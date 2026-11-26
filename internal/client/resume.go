package client

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
)

// stateSuffix names the state file of a partial download: "<file>.ghola".
// The file records which version of the remote file the partial bytes came
// from, so a resume can send If-Range and not join two versions (plan phase
// 2, item 9; the audit found that curl, Wget2 and aria2 all join them).
const stateSuffix = ".ghola"

// resumeState is the content of a state file. URL is kept for root-cause
// analysis: it says where the partial bytes came from. It has no user name
// or password: the state file is not a credential store.
type resumeState struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	// Size and Done are set for a segmented download: the size of the
	// remote file, and the inclusive [start, end] ranges that are in the
	// file. The other bytes may be holes (zeros of --file-allocation).
	Size int64      `json:"size,omitempty"`
	Done [][2]int64 `json:"done,omitempty"`
}

// validator returns the If-Range value: a strong ETag, else Last-Modified.
// A weak ETag cannot be used in If-Range (RFC 9110, section 13.1.5).
func (s resumeState) validator() string {
	if s.ETag != "" && !strings.HasPrefix(s.ETag, "W/") {
		return s.ETag
	}
	return s.LastModified
}

func statePath(file string) string { return file + stateSuffix }

// loadState reads the state file of file. With no state file it returns
// the zero state. A state file that cannot be read is an error: a resume
// without its check could join two versions.
func loadState(file string) (s resumeState, err error) {
	b, err := os.ReadFile(statePath(file))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("read resume state: %w", err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("read resume state %s: %w", statePath(file), err)
	}
	return s, nil
}

// loadIfRange returns the If-Range value for a resume of file, or "" when
// there is no usable state file.
func loadIfRange(file string) (string, error) {
	s, err := loadState(file)
	return s.validator(), err
}

// segmented reports a state file of a segmented download.
func (s resumeState) segmented() bool { return s.Size > 0 }

// donePrefix returns the number of bytes from byte 0 that the finished
// pieces cover without a gap. A resume can append after them; the bytes
// after the first gap may be holes.
func (s resumeState) donePrefix() int64 {
	done := slices.Clone(s.Done)
	slices.SortFunc(done, func(a, b [2]int64) int { return cmp.Compare(a[0], b[0]) })
	var end int64
	for _, p := range done {
		if p[0] > end {
			break
		}
		end = max(end, p[1]+1)
	}
	return min(end, s.Size)
}

// saveState records the version of the response that file now holds. With
// no validator there is nothing to check later, so an old state file is
// removed instead: it would describe another version.
func saveState(file, url string, meta gholatransport.StreamMeta) error {
	s := resumeState{URL: url, ETag: meta.ETag, LastModified: meta.LastModified}
	if s.validator() == "" {
		return removeState(file)
	}
	return writeState(file, s)
}

// writeState writes s as the state file of file. It writes a temporary
// file and renames it, so a kill during the write does not leave half a
// state file.
func writeState(file string, s resumeState) error {
	s.URL = withoutUserinfo(s.URL)
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := statePath(file) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write resume state: %w", config.WriteError(err))
	}
	err = os.Rename(tmp, statePath(file))
	for try := 1; err != nil && try < renameTries; try++ {
		time.Sleep(renameWait) // Windows: a reader of the state file holds it for a moment
		err = os.Rename(tmp, statePath(file))
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write resume state: %w", config.WriteError(err))
	}
	return nil
}

// renameTries and renameWait bound the retries of the state file rename.
const (
	renameTries = 5
	renameWait  = 10 * time.Millisecond
)

// withoutUserinfo returns raw without the user name and password. A value
// that is not a URL is dropped: it could hold them in a form that this
// does not know.
func withoutUserinfo(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}

// pieceMap is the state file of a segmented download. It is written before
// the first byte and after each finished piece, so a killed download
// leaves a record of which bytes are in the file.
type pieceMap struct {
	mu    sync.Mutex
	file  string
	state resumeState
}

func newPieceMap(file, url string, size int64, meta gholatransport.StreamMeta) (*pieceMap, error) {
	p := &pieceMap{file: file, state: resumeState{URL: url, ETag: meta.ETag, LastModified: meta.LastModified, Size: size}}
	return p, writeState(file, p.state)
}

// done records a finished piece. A failed write does not stop the
// download: the state file on disk then names fewer pieces than the file
// holds, and a resume fetches those again. On Windows the rename fails
// while another process reads the state file.
func (p *pieceMap) done(r byteRange) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Done = append(p.state.Done, [2]int64{r.start, r.end})
	_ = writeState(p.file, p.state)
}

// removeState removes the state file of a finished download.
func removeState(file string) error {
	if err := os.Remove(statePath(file)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove resume state: %w", config.WriteError(err))
	}
	return nil
}
