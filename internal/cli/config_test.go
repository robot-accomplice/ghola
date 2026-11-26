package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// configCommand writes text to a config file in a fresh config folder,
// and parses args with it. The folder is the default one, through the
// environment, so a developer's own config cannot leak into the test.
func configCommand(t *testing.T, text string, args ...string) (*Command, error) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	if text != "" {
		if err := os.MkdirAll(filepath.Join(dir, "ghola"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ghola", "config"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	return WithConfig(args, c)
}

const sampleConfig = "# defaults\r\nfollow-max = 10\r\nheader = X-A: 1\r\nquiet\r\nconnections = 4\r\n"

// The config file sets defaults, and the command line wins (v1 spec,
// section 8.2): a single option takes the command line's value, and a
// repeated one gets both. A download-only option in the file does not
// break a fetch: one file serves every job.
func TestWithConfig_CommandLineWins(t *testing.T) {
	c, err := configCommand(t, sampleConfig, "--follow-max", "3", "-H", "X-B: 2", "u")
	if err != nil {
		t.Fatal(err)
	}
	if c.Last("follow-max") != "3" || c.Last("quiet") != "true" || c.Has("connections") {
		t.Errorf("values = %v", c.Values)
	}
	if got := c.Values["header"]; !reflect.DeepEqual(got, []string{"X-A: 1", "X-B: 2"}) {
		t.Errorf("headers = %q", got)
	}
	if !reflect.DeepEqual(c.Args, []string{"u"}) {
		t.Errorf("args = %q", c.Args)
	}
	d, err := configCommand(t, sampleConfig, "download", "u")
	if err != nil {
		t.Fatal(err)
	}
	if d.Job != JobDownload || d.Last("connections") != "4" {
		t.Errorf("download: job %q, values %v", d.Job, d.Values)
	}
}

// --no-config reads no file, a missing default file is no error, and an
// explicit --config that is missing is one.
func TestWithConfig_Files(t *testing.T) {
	c, err := configCommand(t, sampleConfig, "--no-config", "u")
	if err != nil || c.Has("follow-max") {
		t.Errorf("--no-config: %v, %v", err, c)
	}
	if _, err := configCommand(t, "", "u"); err != nil {
		t.Errorf("missing default file: %v", err)
	}
	if _, err := configCommand(t, "", "--config", filepath.Join(t.TempDir(), "none"), "u"); err == nil {
		t.Error("a missing --config file was accepted")
	}
}

// A bad line names the file and the line.
func TestWithConfig_BadLines(t *testing.T) {
	for text, want := range map[string]string{
		"follow-max = 10\nfolow = 2\n": "line 2",
		"config = other\n":             "only on the command line",
		"follow-max = many\n":          "follow-max",
	} {
		_, err := configCommand(t, text, "u")
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "config") {
			t.Errorf("%q: err = %v, want %q", text, err, want)
		}
	}
}

// A config file saved with a UTF-8 BOM reads its first line, and " #"
// after a value starts a comment. Before, the first line was "not a
// ghola option" and "follow-max = 3 # note" was a strconv error.
func TestWithConfig_BOMAndInlineComment(t *testing.T) {
	c, err := configCommand(t, "\ufefffollow-max = 3 # three hops\nreferer = http://r/#top\n", "http://h/")
	if err != nil {
		t.Fatal(err)
	}
	if c.Last("follow-max") != "3" || c.Last("referer") != "http://r/#top" {
		t.Errorf("follow-max %q, referer %q", c.Last("follow-max"), c.Last("referer"))
	}
}

// A bad config line stops only the jobs that read the config: --help,
// --version, explain, completion and profiles still run. Before, they
// all exited 2, so the help could not say what was wrong.
func TestWithConfig_BadLineDoesNotStopHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {"explain", "22"}, {"completion", "bash"}, {"profiles"}} {
		if _, err := configCommand(t, "no-such-option = 1\n", args...); err != nil {
			t.Errorf("%q: %v", args, err)
		}
	}
	if _, err := configCommand(t, "no-such-option = 1\n", "http://h/"); err == nil {
		t.Error("a fetch read a bad config file without an error")
	}
}
