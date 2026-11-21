package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// aria2's input file: a URL on each line, its options on the indented
// lines below, and several URIs on one line are mirrors of one file. Each
// entry keeps its own options, so out= names that file only.
func TestInput_Lines(t *testing.T) {
	path := writeFile(t, "in.txt", "# comment\nhttp://a/1\thttp://m/1\n out=one.bin\n checksum=sha-256=00\n\nhttp://a/2\n")
	entries, err := readInput(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].url != "http://a/1" || entries[1].url != "http://a/2" {
		t.Fatalf("%+v", entries)
	}
	got := strings.Join(entries[0].args, " ")
	for _, want := range []string{"--source http://m/1", "-o one.bin", "--checksum sha-256=00"} {
		if !strings.Contains(got, want) {
			t.Errorf("entry 1 args %q, want %q", got, want)
		}
	}
	if len(entries[1].args) != 0 {
		t.Errorf("entry 2 took entry 1's options: %v", entries[1].args)
	}
	for name, body := range map[string]string{"option first": " out=x\nhttp://a/\n", "bad option": "http://a/\n no-such-aria2-option=1\n"} {
		if _, err := readInput(writeFile(t, "bad.txt", body), "lines"); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// HTML input takes the absolute links of <a> and <img>. A relative link
// is skipped: the file has no base URL.
func TestInput_HTML(t *testing.T) {
	path := writeFile(t, "in.html", `<a href="http://h/a">a</a><img src="https://h/i.png"><a href="rel.html">r</a>`)
	entries, err := readInput(path, "html")
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, e := range entries {
		urls = append(urls, e.url)
	}
	if strings.Join(urls, " ") != "http://h/a https://h/i.png" {
		t.Errorf("%v", urls)
	}
}

func TestInput_Sitemap(t *testing.T) {
	path := writeFile(t, "s.xml", `<urlset><url><loc> http://h/a </loc></url><url><loc></loc></url><url><loc>http://h/b</loc></url></urlset>`)
	entries, err := readInput(path, "sitemap")
	if err != nil || len(entries) != 2 || entries[0].url != "http://h/a" {
		t.Errorf("%+v %v", entries, err)
	}
	if _, err := readInput(writeFile(t, "bad.xml", "<urlset"), "sitemap"); err == nil {
		t.Error("a broken sitemap was accepted")
	}
}

// A Metalink file is one entry: the best URL first, the others as
// mirrors, the strongest hash as --checksum, and its name as -o. A name
// with a folder part is not taken, so a Metalink cannot write outside the
// folder.
func TestInput_Metalink(t *testing.T) {
	v4 := `<metalink xmlns="urn:ietf:params:xml:ns:metalink">
<file name="f.iso"><url priority="2">http://m2/f.iso</url><url priority="1">http://m1/f.iso</url>
<hash type="md5">AA</hash><hash type="sha-256">BB</hash></file>
<file name="../evil"><url>http://m/e</url></file>
<file name="empty"></file></metalink>`
	entries, err := readInput(writeFile(t, "f.meta4", v4), "metalink")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].url != "http://m1/f.iso" {
		t.Fatalf("%+v", entries)
	}
	if got := strings.Join(entries[0].args, " "); got != "--source http://m2/f.iso --checksum sha-256=bb -o f.iso" {
		t.Errorf("args %q", got)
	}
	if strings.Contains(strings.Join(entries[1].args, " "), "evil") {
		t.Errorf("a name with a folder part was taken: %v", entries[1].args)
	}
	v3 := `<metalink version="3.0"><files><file name="g"><resources><url preference="10">http://low/g</url><url preference="90">http://high/g</url></resources>
<verification><hash type="sha1">CC</hash></verification></file></files></metalink>`
	entries, err = readInput(writeFile(t, "g.metalink", v3), "metalink")
	if err != nil || len(entries) != 1 || entries[0].url != "http://high/g" || !strings.Contains(strings.Join(entries[0].args, " "), "--checksum sha-1=cc") {
		t.Errorf("v3: %+v %v", entries, err)
	}
	if _, err := readInput(writeFile(t, "x", "x"), "torrent"); err == nil {
		t.Error("an unknown format was accepted")
	}
	if _, err := readInput(filepath.Join(t.TempDir(), "none"), ""); err == nil {
		t.Error("a missing file was accepted")
	}
}

// --input adds its URLs after the command line's, checks each entry's
// options before any transfer, and ApplyInputArgs puts out= inside the
// run's -o DIR/, as aria2 does.
func TestApply_InputAndEntryOptions(t *testing.T) {
	path := writeFile(t, "in.txt", "http://h/a\n out=named.bin\nhttp://h/b\n")
	dir := t.TempDir() + string(filepath.Separator)
	o, err := Apply(apply(t, "download", "-o", dir, "--input", path, "http://h/first"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.URLs, " ") != "http://h/first http://h/a http://h/b" || len(o.Output.InputArgs) != 3 {
		t.Fatalf("urls %v args %v", o.URLs, o.Output.InputArgs)
	}
	one := *o
	if err := ApplyInputArgs(&one, o.Output.InputArgs[1]); err != nil {
		t.Fatal(err)
	}
	if one.Output.File != filepath.Join(dir, "named.bin") {
		t.Errorf("out= went to %q", one.Output.File)
	}
	if err := ApplyInputArgs(&one, nil); err != nil {
		t.Error(err)
	}
	if _, err := Apply(apply(t, "download", "--input", writeFile(t, "e.txt", "# none\n"))); err == nil {
		t.Error("an --input with no URL and no URL on the command line was accepted")
	}
}

// --next splits a fetch command into groups; after -- it is a URL, and a
// job other than fetch refuses it.
func TestSplitNext(t *testing.T) {
	groups, err := SplitNext([]string{"-q", "http://a", "--next", "-d", "x", "http://b", "--", "--next"})
	if err != nil || len(groups) != 2 || strings.Join(groups[1], " ") != "-d x http://b -- --next" {
		t.Errorf("%q %v", groups, err)
	}
	if _, err := SplitNext([]string{"download", "http://a", "--next", "http://b"}); err == nil {
		t.Error("download took --next")
	}
	if g, err := SplitNext([]string{"curl", "http://a", "--next", "http://b"}); err != nil || len(g) != 1 {
		t.Errorf("ghola curl must keep curl's own -: syntax whole: %q %v", g, err)
	}
	if g, _ := SplitNext([]string{"download", "--", "--next"}); len(g) != 1 {
		t.Error("--next after -- is a URL")
	}
}

// --help lists every group, --help=GROUP lists one, and an unknown group
// names the groups.
func TestHelp(t *testing.T) {
	all, err := Help("1.0", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"TLS:", "Mirror (ghola mirror):", "--alt-svc PATH"} {
		if !strings.Contains(all, title) {
			t.Errorf("--help has no %q", title)
		}
	}
	if strings.Contains(all, "not built yet") {
		t.Error("--help marks an option as not built")
	}
	tls, err := Help("1.0", "tls")
	if err != nil || !strings.Contains(tls, "TLS:") || strings.Contains(tls, "Mirror (ghola mirror):") {
		t.Errorf("--help=tls: %v", err)
	}
	if _, err := Help("1.0", "nope"); err == nil || !strings.Contains(err.Error(), "tls") {
		t.Errorf("--help=nope: %v", err)
	}
	if !strings.Contains(ShortUsage("1.0"), "-o, --output") {
		t.Error("the short usage lists the lettered options")
	}
}

// Setters that the other tests do not reach: each value lands where the
// transfer reads it.
func TestApply_MoreSetters(t *testing.T) {
	o, err := Apply(apply(t, "--no-progress", "--head", "--max-header-size", "64K", "--stall-time", "10s", "--wait-jitter", "1.5s", "--auth", "digest", "-u", "a:b", "u"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Output.Progress != "off" || o.Method != "HEAD" || !o.Output.Include || o.Resilience.BufferSize != 64*1024 ||
		o.Resilience.SpeedTime != 10*time.Second || o.Resilience.SpeedLimit != 1 || o.Stealth.Drift != 1500 || o.Auth != "digest" {
		t.Errorf("%+v", o)
	}
	o, err = Apply(apply(t, "--stall-rate", "1K", "--upload", "f.txt", "u"))
	if err != nil || o.Resilience.SpeedLimit != 1024 || o.Resilience.SpeedTime == 0 || o.Method != "PUT" || o.Transfer != "f.txt" {
		t.Errorf("%+v %v", o, err)
	}
	for _, bad := range []string{"ntlm", "negotiate"} {
		if _, err := Apply(apply(t, "--auth", bad, "u")); err == nil {
			t.Errorf("--auth %s was accepted", bad)
		}
	}
}

// ghola mirror has wget -m's defaults unless the command line sets them,
// and its lists are comma lists.
func TestApply_MirrorDefaults(t *testing.T) {
	o, err := Apply(apply(t, "mirror", "--accept", "*.html, *.css,", "--no-robots", "http://h/"))
	if err != nil {
		t.Fatal(err)
	}
	m := o.Mirror
	if !m.On || m.Depth != mirrorDefaultDepth || !m.Timestamps || m.Robots || strings.Join(m.Accept, "|") != "*.html|*.css" {
		t.Errorf("%+v", m)
	}
	o, err = Apply(apply(t, "mirror", "--depth", "1", "--no-timestamps", "--convert-links", "http://h/"))
	if err != nil || o.Mirror.Depth != 1 || o.Mirror.Timestamps || !o.Mirror.Convert {
		t.Errorf("%+v %v", o.Mirror, err)
	}
}

// --cond-etag-compare sends If-None-Match from the file, but not when the
// file is missing or empty (the first run), and not over a -H header.
func TestApply_ETagCompare(t *testing.T) {
	path := writeFile(t, "etag", "\"v1\"\n")
	o, err := Apply(apply(t, "--cond-etag-compare", path, "u"))
	if err != nil || !hasHeader(o.Headers, "If-None-Match") || !o.Output.CondTime {
		t.Errorf("%v %v", o.Headers, err)
	}
	o, _ = Apply(apply(t, "--cond-etag-compare", path, "-H", "If-None-Match: mine", "u"))
	if n := strings.Count(strings.Join(o.Headers, "\n"), "If-None-Match"); n != 1 {
		t.Errorf("%d If-None-Match headers", n)
	}
	for _, p := range []string{filepath.Join(t.TempDir(), "none"), writeFile(t, "empty", "")} {
		o, err := Apply(apply(t, "--cond-etag-compare", p, "u"))
		if err != nil || hasHeader(o.Headers, "If-None-Match") {
			t.Errorf("%s: %v %v", p, o.Headers, err)
		}
	}
	if _, err := Apply(apply(t, "--cond-etag-compare", t.TempDir(), "u")); err == nil {
		t.Error("a folder as the ETag file was accepted")
	}
}

// One -o - serves several URLs: stdout takes the bodies in order, as curl
// and wget -O - write them. ghola wget maps --retry-on-http-error.
func TestApply_StdoutServesEveryURL(t *testing.T) {
	o, err := Apply(apply(t, "-o", "-", "http://h/a", "http://h/b"))
	if err != nil || !o.Output.Stdout {
		t.Errorf("-o - with two URLs: %v", err)
	}
	if _, err := Apply(apply(t, "-o", "f", "http://h/a", "http://h/b")); err == nil {
		t.Error("one -o FILE for two URLs must still be refused: the second would replace the first")
	}
	cp, err := FromWget([]string{"--retry-on-http-error=503,504", "http://h/"})
	if err != nil || !strings.Contains(strings.Join(cp.Args, " "), "--retry-status 503,504") {
		t.Errorf("%v %v", cp, err)
	}
}
