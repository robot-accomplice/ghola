package main

import (
	"os"
	"strings"
	"testing"
)

// TestMain points the config and state folders at an empty one, so that
// the config file and the job list of the developer who runs the tests
// cannot change their results, and the tests do not write into them.
func TestMain(m *testing.M) {
	if out := os.Getenv("GHOLA_TEST_HOOK_OUT"); out != "" {
		// The test binary is the --on-complete command of TestExecute_Hooks.
		os.WriteFile(out, []byte(strings.Join(os.Args[1:], "|")), 0o600) //nolint:errcheck
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "ghola-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir) //nolint:errcheck
	os.Setenv("APPDATA", dir)         //nolint:errcheck
	os.Setenv("XDG_STATE_HOME", dir)  //nolint:errcheck // the job list of ghola jobs
	os.Setenv("LOCALAPPDATA", dir)    //nolint:errcheck
	code := m.Run()
	os.RemoveAll(dir) //nolint:errcheck
	os.Exit(code)
}
