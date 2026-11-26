package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/jobs"
)

// pause and cancel change only the job list; they cannot stop a transfer
// in another process. So they refuse a job whose file is being written,
// and the running download keeps its file. Before, cancel reported
// success and the download went on, and pause did not stop it.
func TestJobs_PauseAndCancelRefuseARunningJob(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("LOCALAPPDATA", state)
	file := filepath.Join(t.TempDir(), "big.bin")
	running, err := config.OpenOutput(config.OutputOptions{}, file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, false)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	id := jobs.ID("http://h/big.bin", file)
	if err := jobs.Update(func(m map[string]jobs.Job) {
		m[id] = jobs.Job{ID: id, URL: "http://h/big.bin", File: file, Added: time.Now()}
	}); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"pause", "cancel"} {
		if code := runGhola(t, "jobs", verb, id); code == 0 {
			t.Errorf("jobs %s on a running job: exit 0", verb)
		}
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("cancel removed the file of a running job: %v", err)
	}
	if list, _ := jobs.List(); len(list) != 1 || list[0].Paused {
		t.Errorf("the job list changed: %+v", list)
	}
}
