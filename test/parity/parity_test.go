//go:build parity

// Package parity runs ghola and the reference tools (curl, wget or Wget2,
// aria2) on the same jobs against a local hostile server, and checks each
// result against one rule. Run it with:
//
//	go test -tags parity ./test/parity/ -v
//
// See README.md in this directory.
package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var gholaBin string

func TestMain(m *testing.M) {
	code, err := buildAndRun(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parity:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func buildAndRun(m *testing.M) (int, error) {
	if gholaBin = os.Getenv("PARITY_GHOLA"); gholaBin != "" {
		return m.Run(), nil
	}
	dir, err := os.MkdirTemp("", "parity-ghola-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	gholaBin = filepath.Join(dir, "ghola")
	if runtime.GOOS == "windows" {
		gholaBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", gholaBin, "../../cmd/ghola")
	if out, err := build.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("build ghola: %v\n%s", err, out)
	}
	return m.Run(), nil
}

// outDir is where the records go: PARITY_OUT, or test/parity/out.
func outDir(t *testing.T) string {
	dir := os.Getenv("PARITY_OUT")
	if dir == "" {
		dir = "out"
	}
	dir, _ = filepath.Abs(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParity(t *testing.T) {
	out := outDir(t)
	srv, err := newServer(out)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.close()
	tools := findTools(t, gholaBin)
	only := os.Getenv("PARITY_ONLY") // a comma-separated list of scenario IDs

	var results []*Result
	for _, sc := range scenarios {
		if only != "" && !contains(strings.Split(only, ","), sc.ID) {
			continue
		}
		t.Run(sc.ID, func(t *testing.T) {
			results = append(results, runScenario(t, srv, out, sc, tools)...)
		})
	}
	writeRecords(t, out, tools, results)
}

// runScenario runs each tool on one scenario, then each compatibility
// command on the arguments of the tool it copies.
func runScenario(t *testing.T, srv *server, out string, sc scenario, tools map[string]*tool) []*Result {
	t.Helper()
	var results []*Result
	ref := map[string]*Result{}
	for _, name := range toolOrder {
		mk, tl := sc.Runs[name], tools[name]
		if mk == nil || tl == nil {
			continue
		}
		ref[name] = runOne(t, srv, out, sc, tl, mk)
		results = append(results, ref[name])
	}
	for _, name := range compatOrder {
		c := compatTools[name]
		if mk, tl, r := sc.Runs[c.of], tools[name], ref[c.of]; mk != nil && tl != nil && r != nil {
			got := runOne(t, srv, out, sc, tl, mk)
			judgeCompat(t, sc, got, r)
			results = append(results, got)
		}
	}
	return results
}

// runOne runs one tool on one scenario, checks the result, and judges it.
func runOne(t *testing.T, srv *server, out string, sc scenario, tl *tool, mk func(e *Env) Spec) *Result {
	t.Helper()
	key := sc.ID + "." + tl.Name
	env := &Env{s: srv, key: key}
	r, err := execute(tl, mk(env), filepath.Join(out, "runs", key))
	if err != nil {
		t.Fatalf("%s: %v", tl.Name, err)
	}
	r.Scenario, r.Finding = sc.ID, sc.Finding
	r.Requests = srv.requests(key)
	if err := sc.Check(r, env); err != nil {
		r.Reason = err.Error()
	} else {
		r.Pass = true
	}
	switch {
	case tl.Name == "ghola":
		judge(t, sc, r)
	case compatTools[tl.Name].of != "":
		// judged against its tool by judgeCompat
	case !r.Pass:
		t.Logf("%s fails: %s", tl.Name, r.Reason)
	}
	return r
}

// compatTools are the pseudo-tools of the compatibility commands: the
// ghola binary on the arguments of the tool they copy (of), with that
// tool's config switch first, as the tool itself gets it.
var compatTools = map[string]struct {
	of   string
	base []string
}{
	"ghola-curl":  {of: "curl", base: []string{"curl", "-q"}},
	"ghola-wget":  {of: "wget", base: []string{"wget", "--no-config"}},
	"ghola-aria2": {of: "aria2", base: []string{"aria2c", "--no-conf"}},
}

var compatOrder = []string{"ghola-curl", "ghola-wget", "ghola-aria2"}

// judgeCompat requires a compatibility command to pass each scenario that
// its tool passes. Where the tool fails, the command may pass: it keeps
// ghola's fixes, such as If-Range on resume.
func judgeCompat(t *testing.T, sc scenario, r, ref *Result) {
	t.Helper()
	if ref.Pass && !r.Pass {
		t.Errorf("%s fails %s, which %s passes: %s (args %q, exit %d, stderr %q)", r.Tool, sc.ID, ref.Tool, r.Reason, r.Args, r.Exit, firstLine(r.Stderr))
	}
}

// judge applies the ratchet in known_test.go to ghola's result.
func judge(t *testing.T, sc scenario, r *Result) {
	t.Helper()
	why, isKnown := known[sc.ID]
	r.Known = why
	switch {
	case r.Pass && isKnown:
		t.Errorf("ghola now passes %s (%s): remove it from known_test.go", sc.ID, why)
	case !r.Pass && !isKnown:
		t.Errorf("ghola fails %s: %s (args %q, exit %d, stderr %q)", sc.ID, r.Reason, r.Args, r.Exit, firstLine(r.Stderr))
	case !r.Pass:
		t.Logf("ghola fails %s as known (%s): %s", sc.ID, why, r.Reason)
	}
}

// TestScenariosCoverAuditFindings checks that each open audit finding has at
// least one scenario. It does not need the reference tools.
func TestScenariosCoverAuditFindings(t *testing.T) {
	covered := map[string]bool{}
	ids := map[string]bool{}
	for _, sc := range scenarios {
		covered[sc.Finding] = true
		if ids[sc.ID] {
			t.Errorf("duplicate scenario ID %s", sc.ID)
		}
		ids[sc.ID] = true
	}
	for _, f := range []string{"C2", "C3", "H1", "H2", "H3", "H4", "H5", "H6", "H7", "H8",
		"M1", "M2", "M3", "M4", "M5", "M6", "M7", "M8", "M9"} {
		if !covered[f] {
			t.Errorf("audit finding %s has no scenario", f)
		}
	}
	for id := range known {
		if !ids[id] {
			t.Errorf("known_test.go lists %s, but no scenario has that ID", id)
		}
	}
}

func writeRecords(t *testing.T, out string, tools map[string]*tool, results []*Result) {
	t.Helper()
	versions := map[string]string{}
	for name, tl := range tools {
		versions[name] = tl.Version
	}
	rec := map[string]any{"os": runtime.GOOS + "/" + runtime.GOARCH, "tools": versions, "results": results}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "results.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	summary := summarize(tools, results)
	if err := os.WriteFile(filepath.Join(out, "summary.md"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("records in %s\n%s", out, summary)
}

// summarize writes one row per scenario: pass, FAIL, or "-" when the tool
// has no command for it or is not installed.
func summarize(tools map[string]*tool, results []*Result) string {
	cell := map[string]map[string]*Result{}
	var order []string
	for _, r := range results {
		if cell[r.Scenario] == nil {
			cell[r.Scenario] = map[string]*Result{}
			order = append(order, r.Scenario)
		}
		cell[r.Scenario][r.Tool] = r
	}
	var b strings.Builder
	b.WriteString("| Scenario | Finding |")
	for _, n := range toolOrder {
		b.WriteString(" " + n + " |")
	}
	b.WriteString("\n|---|---|" + strings.Repeat("---|", len(toolOrder)) + "\n")
	for _, id := range order {
		row := cell[id]
		var finding string
		for _, r := range row {
			finding = r.Finding
		}
		fmt.Fprintf(&b, "| %s | %s |", id, finding)
		for _, n := range toolOrder {
			switch r := row[n]; {
			case r == nil:
				b.WriteString(" - |")
			case r.Pass:
				b.WriteString(" pass |")
			default:
				b.WriteString(" FAIL |")
			}
		}
		b.WriteString("\n")
	}
	var names []string
	for n := range tools {
		names = append(names, n)
	}
	sort.Strings(names)
	b.WriteString("\nTools:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "- %s: %s (%s)\n", n, tools[n].Version, tools[n].Path)
	}
	return b.String()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.TrimSpace(x) == s {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
