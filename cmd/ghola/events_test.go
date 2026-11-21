package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// eventSchema is the part of docs/cli/output-events.schema.json that the
// test checks each line against: the fields, the fields every line has,
// and the fields of each event.
type eventSchema struct {
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
	AllOf      []struct {
		If struct {
			Properties struct {
				Event struct {
					Const string   `json:"const"`
					Enum  []string `json:"enum"`
				} `json:"event"`
			} `json:"properties"`
		} `json:"if"`
		Then struct {
			Required []string `json:"required"`
		} `json:"then"`
	} `json:"allOf"`
}

func loadEventSchema(t *testing.T) eventSchema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli", "output-events.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s eventSchema
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// check reports the ways a line breaks the schema, so a field that the
// code adds without the schema, or drops while the schema requires it,
// fails the test.
func (s eventSchema) check(t *testing.T, line map[string]any) {
	t.Helper()
	for k := range line {
		if _, ok := s.Properties[k]; !ok {
			t.Errorf("field %q is not in the schema: %v", k, line)
		}
	}
	required := append([]string(nil), s.Required...)
	event, _ := line["event"].(string)
	for _, rule := range s.AllOf {
		e := rule.If.Properties.Event
		if e.Const == event || strings.Contains(strings.Join(e.Enum, " "), event) {
			required = append(required, rule.Then.Required...)
		}
	}
	for _, k := range required {
		if _, ok := line[k]; !ok {
			t.Errorf("%s line has no %q: %v", event, k, line)
		}
	}
}

// --output-events json writes a start line, a retry line for each retry,
// and a done or error line with the exit code (spec decision J). A
// program that drives ghola reads these lines, so their shape is the
// contract: each line must match the schema in the repo.
func TestExecute_OutputEvents(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/flaky" && calls.Add(1) == 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.URL.Path == "/missing":
			http.NotFound(w, r)
		default:
			io.WriteString(w, "ok") //nolint:errcheck
		}
	}))
	defer ts.Close()
	file := filepath.Join(t.TempDir(), "f")
	stderr := captureStderr(t, func() {
		runGhola(t, "-q", "--output-events", "json", "--retry-delay", "0", ts.URL+"/flaky", ts.URL+"/missing")
		runGhola(t, "download", "-q", "--output-events", "json", "-o", file, ts.URL+"/file")
		runGhola(t, "mirror", "-q", "--output-events", "json", "--no-robots", "--depth", "1", "-o", t.TempDir()+"/", ts.URL+"/m")
	})
	schema := loadEventSchema(t)
	var got []string
	for _, l := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if !strings.HasPrefix(l, "{") {
			continue // an error message; the events are the JSON lines
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(l), &line); err != nil {
			t.Fatalf("not a JSON line: %q", l)
		}
		schema.check(t, line)
		path := strings.TrimPrefix(line["url"].(string), ts.URL)
		desc := line["event"].(string) + " " + path
		if c, ok := line["exit_code"]; ok {
			desc += " " + jsonText(c)
		}
		if f, ok := line["file"]; ok && f == file {
			desc += " file"
		}
		got = append(got, desc)
	}
	want := "start /flaky|retry /flaky|done /flaky 0|start /missing|error /missing 22|start /file|done /file 0 file|start /m|done /m 0"
	if strings.Join(got, "|") != want {
		t.Errorf("events\n got %s\nwant %s", strings.Join(got, "|"), want)
	}
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
