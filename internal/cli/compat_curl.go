package cli

import (
	"io"
	"net/url"
	"os"
	"strings"
)

// curl's body options. curl sends one body made of all of them: each part
// joins the parts before it with "&", except a --json part, which joins
// with nothing. ghola curl builds that body and passes it to the native
// parser as one --data-literal, so no part is lost.

// The kinds of a body part.
const (
	dataASCII     = iota // -d, --data, --data-ascii: @file drops CR and LF
	dataBinary           // --data-binary: @file as it is
	dataRaw              // --data-raw: no @file
	dataURLEncode        // --data-urlencode
	dataJSON             // --json: as --data-binary, and joins with nothing
)

var curlDataKinds = map[string]int{
	"-d": dataASCII, "--data": dataASCII, "--data-ascii": dataASCII,
	"--data-binary": dataBinary, "--data-raw": dataRaw,
	"--data-urlencode": dataURLEncode, "--json": dataJSON,
}

// curlBody is the body that the data options of one curl group build.
type curlBody struct {
	set  bool
	text strings.Builder
	json bool // a --json part: send the JSON headers
	get  bool // -G: send the data as the query of a GET
}

// addData adds one data option's part to the body.
func (t *translation) addData(kind int, value string) error {
	part, err := curlDataPart(kind, value)
	if err != nil {
		return usagef("ghola curl: %v", err)
	}
	b := &t.body
	if b.set && kind != dataJSON {
		b.text.WriteByte('&')
	}
	b.set = true
	b.json = b.json || kind == dataJSON
	b.text.WriteString(part)
	return nil
}

// curlDataPart is the text that one data option adds, as curl reads it.
func curlDataPart(kind int, value string) (string, error) {
	switch kind {
	case dataRaw:
		return value, nil
	case dataURLEncode:
		return curlURLEncode(value)
	}
	name, ok := strings.CutPrefix(value, "@")
	if !ok {
		return value, nil
	}
	data, err := readDataFile(name)
	if err != nil || kind != dataASCII {
		return data, err
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(data), nil
}

// curlURLEncode reads curl's --data-urlencode forms: content, =content,
// name=content, @file and name@file. The first = or @ splits the name.
func curlURLEncode(value string) (string, error) {
	i := strings.IndexAny(value, "=@")
	if i < 0 {
		return url.QueryEscape(value), nil
	}
	name, content := value[:i], value[i+1:]
	if value[i] == '@' {
		var err error
		if content, err = readDataFile(content); err != nil {
			return "", err
		}
	}
	if name == "" {
		return url.QueryEscape(content), nil
	}
	return name + "=" + url.QueryEscape(content), nil
}

// readDataFile reads a data file; "-" is stdin.
func readDataFile(name string) (string, error) {
	if name == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(name)
	return string(b), err
}

// curlFinish adds the body that the data options built: as the query of
// each URL with -G, else as one --data-literal. curl -d "" posts an empty
// body, so an empty body sets the method POST in front, where -X wins. A
// body that is not empty sets no method, so a redirect changes the POST
// to a GET as curl -d does.
func curlFinish(t *translation) {
	b := &t.body
	if !b.set {
		return
	}
	data := b.text.String()
	if b.get {
		for i, u := range t.tail {
			if u != "-" {
				t.tail[i] = u + querySep(u) + data
			}
		}
		return
	}
	if data == "" {
		defaults := len(t.spec.defaults)
		t.out = append(t.out[:defaults], append([]string{"-m", "POST"}, t.out[defaults:]...)...)
	}
	t.out = append(t.out, "--data-literal", data)
	if b.json {
		for _, h := range []string{"Content-Type", "Accept"} {
			if !hasHeader(t.headers(), h) {
				t.out = append(t.out, "-H", h+": application/json")
			}
		}
	}
}

func querySep(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

// headers are the -H values of the native arguments so far.
func (t *translation) headers() []string {
	var out []string
	for i := 0; i+1 < len(t.out); i++ {
		if t.out[i] == "-H" {
			out = append(out, t.out[i+1])
		}
	}
	return out
}

// variable reads curl's --variable for the body options, which ghola curl
// expands itself, and passes the value on as NAME=VALUE, so the native
// parser does not read a file or stdin a second time.
func (t *translation) variable(arg string) error {
	if err := t.vars.set(arg); err != nil {
		return usagef("ghola curl: %v", err)
	}
	name := strings.TrimPrefix(arg, "%")
	if i := strings.IndexAny(name, "=@"); i >= 0 {
		name = name[:i]
	}
	t.out = append(t.out, "--variable", name+"="+t.vars[name])
	return nil
}
