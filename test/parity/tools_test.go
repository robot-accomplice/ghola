//go:build parity

package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// toolOrder is the order of the tools in the records and the summary.
var toolOrder = []string{"ghola", "curl", "wget", "aria2"}

type tool struct {
	Name    string
	Path    string
	Version string   // first line of --version
	base    []string // arguments that turn off user config files
}

// findTools finds the reference tools. PARITY_CURL, PARITY_WGET and
// PARITY_ARIA2 override the search. A tool that is not found is skipped and
// the summary says so.
func findTools(t *testing.T, ghola string) map[string]*tool {
	t.Helper()
	found := map[string]*tool{"ghola": {Name: "ghola", Path: ghola}}
	search := map[string][]string{
		"curl":  {"curl"},
		"wget":  {"wget2", "wget"},
		"aria2": {"aria2c"},
	}
	for name, bins := range search {
		path := locate(name, bins)
		if path == "" {
			t.Logf("%s: not found, its scenarios are skipped", name)
			continue
		}
		found[name] = &tool{Name: name, Path: path}
	}
	// The compatibility commands run the arguments of the tool they copy on
	// the ghola binary, with that tool's config switch first (v1 spec,
	// section 6).
	for name, c := range compatTools {
		found[name] = &tool{Name: name, Path: ghola, base: c.base, Version: "ghola " + c.base[0]}
	}
	for _, tl := range found {
		if _, isCompat := compatTools[tl.Name]; isCompat {
			continue
		}
		tl.Version = version(tl.Path)
		switch {
		case tl.Name == "curl":
			tl.base = []string{"-q"} // must be first: do not read .curlrc
		case tl.Name == "aria2":
			tl.base = []string{"--no-conf"}
		case tl.Name == "wget" && strings.Contains(strings.ToLower(tl.Version), "wget2"):
			tl.base = []string{"--no-config"}
		}
	}
	return found
}

// locate returns PARITY_<NAME>, or the first of bins on PATH or in winget.
func locate(name string, bins []string) string {
	if path := os.Getenv("PARITY_" + strings.ToUpper(name)); path != "" {
		return path
	}
	for _, b := range bins {
		if p, err := exec.LookPath(b); err == nil {
			return p
		}
		if p := wingetPath(b); p != "" {
			return p
		}
	}
	return ""
}

// wingetPath finds a tool that winget installed without a PATH link.
func wingetPath(bin string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Packages")
	for _, pattern := range []string{"*/" + bin + ".exe", "*/*/" + bin + ".exe"} {
		if m, _ := filepath.Glob(filepath.Join(root, pattern)); len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

func version(path string) string {
	out, _ := exec.Command(path, "--version").CombinedOutput()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}
