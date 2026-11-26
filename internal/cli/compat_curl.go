package cli

import (
	"cmp"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
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
	curlBodyFinish(t)
	curlOutputs(t)
}

func curlBodyFinish(t *translation) {
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

// headers are the -H values of the native arguments so far, without the
// tool's defaults.
func (t *translation) headers() []string {
	var out []string
	for i := len(t.spec.defaults); i+1 < len(t.out); i++ {
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

// curlOutput is the output state of one curl group. curl pairs each -o
// and -O with the next URL in order; a URL without one goes to stdout,
// or under its remote name with --remote-name-all.
type curlOutput struct {
	outputs       []string // a file name, or "" for -O (the remote name)
	dir           string   // --output-dir
	remoteNameAll bool
	upload        string // -T
	parallel      bool   // -Z
	parallelMax   string
}

// curlOutputOption handles the curl options that depend on the URLs or
// on each other, and the --no- form of a curl on/off option. It returns
// false for other options.
func curlOutputOption(t *translation, name, value string) (bool, error) {
	c := &t.output
	switch name {
	case "-o", "--output":
		c.outputs = append(c.outputs, value)
	case "-O", "--remote-name":
		c.outputs = append(c.outputs, "")
	case "--remote-name-all":
		c.remoteNameAll = true
	case "--output-dir":
		c.dir = value
	case "-T", "--upload-file":
		c.upload = value
		t.out = append(t.out, "--upload", value)
	case "-Z", "--parallel":
		c.parallel = true
	case "--parallel-max":
		c.parallelMax = value
	case "-e", "--referer":
		return true, curlReferer(t, value)
	default:
		return curlNegated(t, name)
	}
	return true, nil
}

// curlReferer reads -e: ";auto" sends the previous URL after a redirect,
// as native --referer auto does. URL;auto has no native form.
func curlReferer(t *translation, value string) error {
	switch {
	case value == ";auto":
		value = "auto"
	case strings.HasSuffix(value, ";auto"):
		return usagef("ghola curl: -e %s is not supported: use -e URL, or -e ';auto' for the previous URL after a redirect", value)
	}
	t.out = append(t.out, "--referer", value)
	return nil
}

// curlNegated translates curl's --no-NAME of an on/off option that maps
// to one native on/off option: --no-location is --no-follow, and
// --no-insecure is --tls-verify.
func curlNegated(t *translation, name string) (bool, error) {
	rest, ok := strings.CutPrefix(name, "--no-")
	if !ok {
		return false, nil
	}
	f, ok := t.spec.flags["--"+rest]
	if !ok || f.value || len(f.args) != 1 || !strings.HasPrefix(f.args[0], "--") {
		return false, nil
	}
	native := f.args[0]
	if positive, ok := strings.CutPrefix(native, "--no-"); ok {
		native = "--" + positive
	} else {
		native = "--no-" + native[2:]
	}
	t.out = append(t.out, native)
	return true, nil
}

// curlOutputs adds the -o of each URL, the file name of -T to a URL that
// ends in /, and --jobs for -Z.
func curlOutputs(t *translation) {
	c := &t.output
	if c.upload != "" && c.upload != "-" {
		for i, u := range t.tail {
			if strings.HasSuffix(u, "/") {
				t.tail[i] = u + url.PathEscape(filepath.Base(c.upload)) // curl -T f URL/ uploads to URL/f
			}
		}
	}
	outs := c.outputs
	switch {
	case len(outs) == 0 && c.remoteNameAll:
		outs = []string{""} // one -o DIR/ serves every URL
	case len(outs) > 0 && len(outs) < len(t.tail) && !hasGlob(t.tail):
		pad := "-"
		if c.remoteNameAll {
			pad = ""
		}
		for len(outs) < len(t.tail) {
			outs = append(outs, pad)
		}
	}
	toStdout := len(outs) == 0
	for i, o := range outs {
		u := ""
		if len(outs) == len(t.tail) {
			u = t.tail[i]
		}
		t.out = append(t.out, "-o", c.target(o, u))
		toStdout = toStdout || o == "-"
	}
	// Bodies on stdout go one after the other: ghola writes stdout from one
	// transfer at a time, and the bytes are the same.
	if c.parallel && !toStdout {
		t.out = append(t.out, "--jobs", cmp.Or(c.parallelMax, curlParallelMax))
	}
}

// curlParallelMax is curl's default number of parallel transfers.
const curlParallelMax = "50"

// target is the native -o of one output. -O on a URL without a file name
// writes curl_response, as curl 8 does.
func (c *curlOutput) target(o, u string) string {
	dir := c.dir
	switch {
	case o == "-":
		return o
	case o == "" && u != "" && remoteNameEmpty(u):
		o = curlNoName
	case o == "":
		return cmp.Or(dir, ".") + "/"
	case filepath.IsAbs(o):
		return o
	}
	if dir == "" {
		return o
	}
	return strings.TrimSuffix(dir, "/") + "/" + o
}

// curlNoName is the file name of curl -O for a URL without one.
const curlNoName = "curl_response"

// remoteNameEmpty reports a URL whose path has no file name.
func remoteNameEmpty(u string) bool {
	p, err := url.Parse(u)
	return err == nil && path.Base("/"+p.Path) == "/" || err == nil && strings.HasSuffix(p.Path, "/")
}

// hasGlob reports a URL with a curl glob, which can stand for several
// URLs.
func hasGlob(urls []string) bool {
	for _, u := range urls {
		if strings.ContainsAny(u, "[{") {
			return true
		}
	}
	return false
}

// curlECH reads curl --ech: true and hard turn ECH on (ghola has no soft
// mode), false turns it off.
func curlECH(t *translation, value string) error {
	switch value {
	case "true", "hard":
		t.out = append(t.out, "--tls-ech")
	case "false":
		t.out = append(t.out, "--no-tls-ech")
	default:
		return usagef("ghola curl: --ech %s is not supported: use true, hard or false", value)
	}
	return nil
}
