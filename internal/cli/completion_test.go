package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Each script names every option and every job, so completion cannot
// lag behind the option table.
func TestCompletion_NamesEveryOption(t *testing.T) {
	_, options := completionWords()
	for shell := range completionShells {
		script, err := Completion(shell)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range append(options, "download", "mirror", "curl") {
			name := strings.TrimPrefix(o, "--")
			if shell == "fish" {
				name = strings.TrimPrefix(name, "no-") // fish completes --no- forms by itself
			}
			if !strings.Contains(script, name) {
				t.Errorf("%s: %s is missing", shell, o)
				break
			}
		}
	}
	if _, err := Completion("tcsh"); err == nil {
		t.Error("an unknown shell was accepted")
	}
}

// The bash and PowerShell scripts parse in their shells. A shell that is
// not installed skips its check, and the test log says so.
func TestCompletion_ScriptsParse(t *testing.T) {
	dir := t.TempDir()
	checks := map[string][]string{
		"bash":       {"bash", "-n"},
		"powershell": {"pwsh", "-NoProfile", "-Command"}, // the parse command gets the path below
	}
	for shell, cmd := range checks {
		if _, err := exec.LookPath(cmd[0]); err != nil {
			t.Logf("%s is not installed: its script is not parsed", cmd[0])
			continue
		}
		script, _ := Completion(shell)
		path := filepath.Join(dir, shell+".script.ps1")
		os.WriteFile(path, []byte(script), 0o644) //nolint:errcheck
		args := append(cmd[1:], path)
		if shell == "powershell" {
			// -Command does not pass extra arguments as $args, so the path
			// goes into the command.
			args = append(cmd[1:], "$t=$null; $e=$null; [void][System.Management.Automation.Language.Parser]::ParseFile('"+path+"', [ref]$t, [ref]$e); if ($e) { $e | ForEach-Object Message; exit 1 }")
		}
		if out, err := exec.Command(cmd[0], args...).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse its script: %v\n%s", cmd[0], err, out)
		}
	}
}

// The words: no --no-no-config, and the --proxy-tls-* forms of the TLS
// options are there. Before, the scripts listed --no-no-config and none
// of the --proxy-tls-* options.
func TestCompletion_Words(t *testing.T) {
	_, options := completionWords()
	all := " " + strings.Join(options, " ") + " "
	for _, want := range []string{" --proxy-tls-ca ", " --proxy-tls-pin ", " --no-proxy-tls-verify ", " --no-config "} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q", want)
		}
	}
	if strings.Contains(all, "--no-no-") || strings.Contains(all, "*") {
		t.Errorf("bad words in %s", all)
	}
}

// PowerShell offers the job words only for the first argument. Before,
// "ghola download " offered the job words again.
func TestCompletion_PowerShellJobsOnlyFirst(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh is not installed")
	}
	script, _ := Completion("powershell")
	path := filepath.Join(t.TempDir(), "c.ps1")
	os.WriteFile(path, []byte(script), 0o644) //nolint:errcheck
	complete := func(line string) string {
		cmd := ". '" + path + "'; (TabExpansion2 -inputScript '" + line + "' -cursorColumn " + strconv.Itoa(len(line)) +
			").CompletionMatches | ForEach-Object CompletionText"
		out, err := exec.Command("pwsh", "-NoProfile", "-Command", cmd).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return string(out)
	}
	if got := complete("ghola dow"); !strings.Contains(got, "download") {
		t.Errorf("ghola dow: %q", got)
	}
	if got := complete("ghola download "); strings.Contains(got, "mirror") {
		t.Errorf("ghola download: job words offered: %q", got)
	}
}
