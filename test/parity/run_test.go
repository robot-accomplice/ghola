//go:build parity

package parity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runTimeout stops a tool that hangs. Scenarios that test timeouts expect
// the tool to stop well before this.
const runTimeout = 30 * time.Second

// keepStdout is the largest stdout that a Result keeps in memory for checks.
// Larger output is checked by its SHA-256 only.
const keepStdout = 8 << 20

// stderrKeep is how much stderr a record keeps. The full stderr stays in the
// run directory.
const stderrKeep = 16 << 10

// Spec is one tool command in one scenario.
type Spec struct {
	Args []string
	Env  []string // added to a clean environment
	Seed []byte   // if set, written to the file "f" before the run
	// SeedFiles are more files to write in the working directory before the
	// run, for example the state file that an earlier ghola run left.
	SeedFiles map[string][]byte
	// KillAfter kills the run after this time, as a user stops a job.
	// Rerun is then the next command in the same folder, and its run is
	// the result. (Plan section 6: every stopped download can restart.)
	KillAfter time.Duration
	Rerun     []string
}

type fileInfo struct {
	Len int64  `json:"len"`
	SHA string `json:"sha256"`
}

// Result is the record of one tool run. It is written to results.json, so a
// failure can be checked after the run without running it again.
type Result struct {
	Scenario  string              `json:"scenario"`
	Finding   string              `json:"finding"`
	Tool      string              `json:"tool"`
	Version   string              `json:"version"`
	Args      []string            `json:"args"`
	Exit      int                 `json:"exit"`
	TimedOut  bool                `json:"timed_out"`
	ElapsedMS int64               `json:"elapsed_ms"`
	PeakRSS   int64               `json:"peak_rss_bytes"` // 0 means not measured
	StdoutLen int64               `json:"stdout_len"`
	StdoutSHA string              `json:"stdout_sha256"`
	Stderr    string              `json:"stderr"`
	Files     map[string]fileInfo `json:"files"` // relative to the work root
	Requests  []reqLog            `json:"requests"`
	Pass      bool                `json:"pass"`
	Reason    string              `json:"reason,omitempty"`
	Known     string              `json:"known_failure,omitempty"`
	Dir       string              `json:"dir"` // stdout, stderr and the work tree

	stdout []byte
	seed   []byte
	work   string // the tool's working directory (work root + /a/b)
}

// execute runs one tool command in dir. The working directory is
// dir/work/a/b, so a tool that writes "../../x" stays inside dir/work.
func execute(tl *tool, spec Spec, dir string) (*Result, error) {
	r := &Result{Tool: tl.Name, Version: tl.Version, Dir: dir, seed: spec.Seed}
	r.work = filepath.Join(dir, "work", "a", "b")
	if err := os.MkdirAll(r.work, 0o755); err != nil {
		return nil, err
	}
	if spec.Seed != nil {
		if err := os.WriteFile(filepath.Join(r.work, "f"), spec.Seed, 0o644); err != nil {
			return nil, err
		}
	}
	for name, b := range spec.SeedFiles {
		if err := os.WriteFile(filepath.Join(r.work, name), b, 0o644); err != nil {
			return nil, err
		}
	}
	stdout, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		return nil, err
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		return nil, err
	}
	defer stderr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	if spec.KillAfter > 0 {
		first := exec.CommandContext(ctx, tl.Path, append(append([]string(nil), tl.base...), spec.Args...)...)
		first.Dir, first.Stdout, first.Stderr = r.work, io.Discard, io.Discard
		first.Env = append(cleanEnv(dir), spec.Env...)
		if err := first.Start(); err != nil {
			return nil, err
		}
		stop := time.AfterFunc(spec.KillAfter, func() { first.Process.Kill() }) //nolint:errcheck
		first.Wait()                                                            //nolint:errcheck // killed on purpose
		stop.Stop()
		spec.Args = spec.Rerun
	}
	r.Args = append(append([]string(nil), tl.base...), spec.Args...)
	cmd := exec.CommandContext(ctx, tl.Path, r.Args...)
	cmd.Dir = r.work
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Env = append(cleanEnv(dir), spec.Env...)
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	peak := startProbe(cmd.Process)
	waitErr := cmd.Wait()
	r.ElapsedMS = time.Since(start).Milliseconds()
	r.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	r.Exit = cmd.ProcessState.ExitCode()
	r.PeakRSS = peak(cmd.ProcessState)
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) && !r.TimedOut {
		return nil, waitErr
	}

	if err := r.readOutput(dir); err != nil {
		return nil, err
	}
	r.Files, err = hashTree(filepath.Join(dir, "work"))
	return r, err
}

func (r *Result) readOutput(dir string) error {
	f, err := os.Open(filepath.Join(dir, "stdout"))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	r.StdoutLen, r.StdoutSHA = n, hex.EncodeToString(h.Sum(nil))
	if n <= keepStdout {
		if r.stdout, err = os.ReadFile(f.Name()); err != nil {
			return err
		}
	}
	e, err := os.ReadFile(filepath.Join(dir, "stderr"))
	if err != nil {
		return err
	}
	if len(e) > stderrKeep {
		e = e[:stderrKeep]
	}
	r.Stderr = string(e)
	return nil
}

// file returns the content of a file in the tool's working directory.
func (r *Result) file(name string) ([]byte, bool) {
	b, err := os.ReadFile(filepath.Join(r.work, name))
	return b, err == nil
}

func hashTree(root string) (map[string]fileInfo, error) {
	files := map[string]fileInfo{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = fileInfo{Len: n, SHA: hex.EncodeToString(h.Sum(nil))}
		return nil
	})
	return files, err
}

// cleanEnv is the environment without proxy settings and with config
// directories inside dir, so the user's own settings cannot change a result.
func cleanEnv(dir string) []string {
	drop := map[string]bool{
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
		"CURL_HOME": true, "WGETRC": true, "XDG_CONFIG_HOME": true, "APPDATA": true, "XDG_STATE_HOME": true, "LOCALAPPDATA": true,
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[strings.ToUpper(k)] {
			env = append(env, kv)
		}
	}
	// ghola reads its config under XDG_CONFIG_HOME (APPDATA on Windows) and
	// keeps its job list under XDG_STATE_HOME (LOCALAPPDATA).
	return append(env, "CURL_HOME="+dir, "XDG_CONFIG_HOME="+dir, "APPDATA="+dir, "XDG_STATE_HOME="+dir, "LOCALAPPDATA="+dir, "WGETRC="+os.DevNull)
}
