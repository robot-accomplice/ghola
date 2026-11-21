package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// TransferInfo is what one transfer did, for --output-template. The
// client fills the response part; main fills the time and the result.
type TransferInfo struct {
	URL          string // the URL as given
	EffectiveURL string // the last URL, after redirects
	Status       int
	ContentType  string
	Redirects    int
	Size         int64 // body bytes that ghola received and wrote
	File         string
	Time         time.Duration
	ExitCode     int
	ErrorMsg     string
}

// templateVars are curl's -w variables that ghola has. %{json} holds
// them all; %{stdout} and %{stderr} switch the output.
var templateVars = map[string]func(TransferInfo) any{
	"url":                func(i TransferInfo) any { return i.URL },
	"url_effective":      func(i TransferInfo) any { return i.EffectiveURL },
	"http_code":          func(i TransferInfo) any { return fmt.Sprintf("%03d", i.Status) },
	"response_code":      func(i TransferInfo) any { return i.Status },
	"content_type":       func(i TransferInfo) any { return i.ContentType },
	"num_redirects":      func(i TransferInfo) any { return i.Redirects },
	"size_download":      func(i TransferInfo) any { return i.Size },
	"filename_effective": func(i TransferInfo) any { return i.File },
	"time_total":         func(i TransferInfo) any { return strconv.FormatFloat(i.Time.Seconds(), 'f', 6, 64) },
	"exitcode":           func(i TransferInfo) any { return i.ExitCode },
	"errormsg":           func(i TransferInfo) any { return i.ErrorMsg },
}

// Template is a parsed --output-template.
type Template struct{ parts []templatePart }

type templatePart struct {
	text string // literal text, when name is ""
	name string
}

// ParseTemplate reads a curl -w template: %{name} variables, %% for %,
// and \n, \r, \t and \\. "@FILE" reads the template from FILE. An unknown
// variable is an error: curl only warns, and the output is then wrong.
func ParseTemplate(s string) (*Template, error) {
	if path, ok := strings.CutPrefix(s, "@"); ok {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--output-template: %w", err)
		}
		s = string(b)
	}
	t := &Template{}
	var text strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "%%"):
			text.WriteByte('%')
			i++
		case strings.HasPrefix(s[i:], "%{"):
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return nil, fmt.Errorf("--output-template: %q has no closing }", s[i:])
			}
			name := s[i+2 : i+end]
			if _, ok := templateVars[name]; !ok && name != "json" && name != "stdout" && name != "stderr" {
				return nil, fmt.Errorf("--output-template: %%{%s} is not a variable ghola has", name)
			}
			t.parts = append(t.parts, templatePart{text: text.String()}, templatePart{name: name})
			text.Reset()
			i += end
		case s[i] == '\\' && i+1 < len(s) && strings.ContainsRune(`nrt\`, rune(s[i+1])):
			text.WriteByte(map[byte]byte{'n': '\n', 'r': '\r', 't': '\t', '\\': '\\'}[s[i+1]])
			i++
		default:
			text.WriteByte(s[i])
		}
	}
	t.parts = append(t.parts, templatePart{text: text.String()})
	return t, nil
}

// AddSize counts body bytes that ghola wrote, for %{size_download}. It is
// atomic: the RPC reads the count (LoadSize) while the download runs.
func (i *TransferInfo) AddSize(n int64) {
	if i != nil {
		atomic.AddInt64(&i.Size, n)
	}
}

// LoadSize reads the count while the download may still add to it.
func (i *TransferInfo) LoadSize() int64 { return atomic.LoadInt64(&i.Size) }

// Write renders the template for info to stdout, or to stderr after
// %{stderr}.
func (t *Template) Write(stdout, stderr io.Writer, info TransferInfo) error {
	w := stdout
	for _, p := range t.parts {
		var s string
		switch p.name {
		case "":
			s = p.text
		case "stdout":
			w = stdout
		case "stderr":
			w = stderr
		case "json":
			all := map[string]any{}
			for name, get := range templateVars {
				all[name] = get(info)
			}
			b, err := json.Marshal(all)
			if err != nil {
				return err
			}
			s = string(b)
		default:
			s = fmt.Sprint(templateVars[p.name](info))
		}
		if _, err := io.WriteString(w, s); err != nil {
			return WriteError(err)
		}
	}
	return nil
}
