package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
)

// stateSuffix names the state file of a partial download: "<file>.ghola".
// The file records which version of the remote file the partial bytes came
// from, so a resume can send If-Range and not join two versions (plan phase
// 2, item 9; the audit found that curl, Wget2 and aria2 all join them).
const stateSuffix = ".ghola"

// resumeState is the content of a state file. URL is kept for root-cause
// analysis: it says where the partial bytes came from.
type resumeState struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
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

// loadIfRange returns the If-Range value for a resume of file, or "" when
// there is no usable state file. A state file that cannot be read is an
// error: a resume without its check could join two versions.
func loadIfRange(file string) (string, error) {
	b, err := os.ReadFile(statePath(file))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read resume state: %w", err)
	}
	var s resumeState
	if err := json.Unmarshal(b, &s); err != nil {
		return "", fmt.Errorf("read resume state %s: %w", statePath(file), err)
	}
	return s.validator(), nil
}

// saveState records the version of the response that file now holds. With
// no validator there is nothing to check later, so an old state file is
// removed instead: it would describe another version.
func saveState(file, url string, meta gholatransport.StreamMeta) error {
	s := resumeState{URL: url, ETag: meta.ETag, LastModified: meta.LastModified}
	if s.validator() == "" {
		return removeState(file)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(statePath(file), b, 0o644); err != nil {
		return fmt.Errorf("write resume state: %w", config.WriteError(err))
	}
	return nil
}

// removeState removes the state file of a finished download.
func removeState(file string) error {
	if err := os.Remove(statePath(file)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove resume state: %w", config.WriteError(err))
	}
	return nil
}
