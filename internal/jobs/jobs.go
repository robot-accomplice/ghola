// Package jobs keeps the list of unfinished downloads, so that
// "ghola jobs resume" can restart them (v1 spec, phase 3: a saved
// session, as aria2 --save-session has and curl and wget do not).
package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

// Job is one download that has not finished.
type Job struct {
	ID     string    `json:"id"`
	URL    string    `json:"url"`
	File   string    `json:"file"` // absolute path
	Args   []string  `json:"args"` // the native options of the run, as --name=value
	Paused bool      `json:"paused,omitempty"`
	Added  time.Time `json:"added"`
}

// ID is a stable 16-digit hex ID of a download, as aria2's GID: the same
// URL and file give the same ID.
func ID(url, file string) string {
	sum := sha256.Sum256([]byte(url + "\x00" + file))
	return hex.EncodeToString(sum[:8])
}

// Path is the job file: $XDG_STATE_HOME/ghola/jobs.json, else
// ~/.local/state/ghola/jobs.json, or %LOCALAPPDATA%\ghola\jobs.json on
// Windows.
func Path() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if runtime.GOOS == "windows" {
		dir = os.Getenv("LOCALAPPDATA")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "ghola", "jobs.json"), nil
}

// mu keeps the parallel jobs of one run from losing each other's change.
// Two ghola processes at the same time can still lose one; the file is
// written whole, with a rename, so it is never half written.
var mu sync.Mutex

// List returns the jobs, oldest first.
func List() ([]Job, error) {
	mu.Lock()
	defer mu.Unlock()
	m, _, err := load()
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(m))
	for _, j := range m {
		out = append(out, j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Added.Before(out[b].Added) })
	return out, nil
}

// Update loads the jobs, lets fn change them, and saves them.
func Update(fn func(map[string]Job)) error {
	mu.Lock()
	defer mu.Unlock()
	m, path, err := load()
	if err != nil {
		return err
	}
	fn(m)
	return save(path, m)
}

func load() (map[string]Job, string, error) {
	path, err := Path()
	if err != nil {
		return nil, "", fmt.Errorf("jobs: %w", err)
	}
	m := map[string]Job{}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, path, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("jobs: %w", err)
	}
	var list []Job
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, "", fmt.Errorf("jobs: %s is not a job file: %w", path, err)
	}
	for _, j := range list {
		m[j.ID] = j
	}
	return m, path, nil
}

func save(path string, m map[string]Job) error {
	list := make([]Job, 0, len(m))
	for _, j := range m {
		list = append(list, j)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Added.Before(list[b].Added) })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	return nil
}
