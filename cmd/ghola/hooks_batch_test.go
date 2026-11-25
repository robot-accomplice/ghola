package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A .bat or .cmd hook runs through cmd.exe, which reads the command line
// again. The file path comes from the server, so cmd.exe must get it as
// one quoted argument: "&" in a name must not start a second command. A
// script that strips the quotes itself (%~3) is the script's own risk.
func TestRunHook_BatchFileGetsNameAsOneArgument(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe runs .bat files only on Windows")
	}
	dir := t.TempDir()
	hook := filepath.Join(dir, "hook.bat")
	record := filepath.Join(dir, "args.txt")
	script := "@echo [%1] [%2] [%3]> \"" + record + "\"\r\n" // %3 keeps the quotes ghola sends; %~3 would drop them
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, name := range []string{"a&mkdir,PWNED&.bin", "b|mkdir,PWNED2.bin", "c^&mkdir,PWNED3.bin", "d(x).bin"} {
		os.Remove(record) //nolint:errcheck
		opts := &config.Options{URL: "http://h/f"}
		opts.Output.File = filepath.Join(dir, name)
		opts.Output.Silent = true
		runHook(hook, opts)
		for _, bad := range []string{"PWNED", "PWNED2", "PWNED3"} {
			if _, err := os.Stat(filepath.Join(dir, bad)); err == nil {
				t.Fatalf("name %q ran a command: %s exists", name, bad)
			}
		}
		got, err := os.ReadFile(record)
		if err != nil || !strings.Contains(string(got), `["`+opts.Output.File+`"]`) {
			t.Errorf("name %q: hook got %q, %v", name, got, err)
		}
	}
}

// cmd.exe expands %VAR% even inside quotes, so a name with "%" cannot
// reach a batch file safely. ghola does not run the hook then.
func TestRunHook_BatchFileRefusesPercent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe runs .bat files only on Windows")
	}
	dir := t.TempDir()
	hook := filepath.Join(dir, "hook.bat")
	record := filepath.Join(dir, "ran.txt")
	if err := os.WriteFile(hook, []byte("@echo ran> \""+record+"\"\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := &config.Options{URL: "http://h/f"}
	opts.Output.File = filepath.Join(dir, "x%PATH%.bin")
	opts.Output.Silent = true
	runHook(hook, opts)
	if _, err := os.Stat(record); err == nil {
		t.Error("the hook ran with a name that cmd.exe expands")
	}
}
