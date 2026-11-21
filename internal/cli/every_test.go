package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// notInApply are the options that Apply does not read, with where they
// act instead.
var notInApply = map[string]string{
	"help": "the caller", "version": "the caller", "next": "SplitNext", "proxy-tls-*": "the family row",
	"config": "WithConfig", "no-config": "WithConfig", "variable": "the parser (--expand-NAME)",
	"input-format": "--input, which it reads (TestInput_*)",
}

// sampleValue is a value of the option's type that differs from its
// default.
func sampleValue(t *testing.T, o *Option, dir string) []string {
	t.Helper()
	file := filepath.Join(dir, "sample")
	os.WriteFile(file, []byte("\"etag\"\nsession=1\n"), 0o600) //nolint:errcheck
	byName := map[string]string{
		"method": "PUT", "output-name": "header", "output-events": "json", "trace-format": "ascii", "referer": "auto",
		"range": "0-9", "continue-at": "5", "chain": "base", "input-format": "html", "http": "1.1", "cond-time": "2026-01-01",
		"ip": "6", "resolve": "h:443:127.0.0.1", "connect-to": "h:443:g:8443", "interface": "lo", "local-port": "4000-4010",
		"tls-cert-type": "DER", "tls-pin": "sha256//AAAA", "auth": "digest", "auth-aws-sigv4": "aws:amz:r:s",
		"proxy-strategy": "random", "impersonate": "firefox", "file-allocation": "trunc", "layout": "flat",
		"file-names": "ascii", "tls-min": "1.3", "tls-max": "1.2", "checksum": "md5=00000000000000000000000000000000",
		"retry-status": "500", "tls-ciphers": "TLS_AES_128_GCM_SHA256", "proxy-bypass": "h", "hosts": "h", "include-dirs": "/a",
		"exclude-dirs": "/b", "accept-regex": "a", "reject-regex": "b", "output-template": "%{http_code}", "output-select": "a.b",
		"cookie": "a=b", "header": "X-A: 1", "doh": "https://dns.example/q", "proxy": "http://p:1", "source": "http://m/f",
		"input": file, "cond-etag-compare": file, "output": "f.out", "output-dir": dir, "user": "a:b", "proxy-user": "a:b",
		"form": "a=b", "field": "a=b", "query": "a=b", "data": "x", "json": "{}", "data-literal": "x", "upload": file,
		"tls-ech": "true", "on-complete": "true", "on-error": "true",
	}
	if v, ok := byName[o.Name]; ok {
		return []string{"--" + o.Name, v}
	}
	switch {
	case o.Type == typeBool && o.Default == "true":
		return []string{"--no-" + o.Name}
	case o.Type == typeBool:
		return []string{"--" + o.Name}
	case o.Type == "duration":
		return []string{"--" + o.Name, "7s"}
	case o.Type == "int":
		return []string{"--" + o.Name, "7"}
	case o.Type == "size" || o.Type == "rate":
		return []string{"--" + o.Name, "7K"}
	case o.Type == "path":
		return []string{"--" + o.Name, filepath.Join(dir, o.Name)}
	case o.Type == "glob list" || o.Type == "list":
		return []string{"--" + o.Name, "*.x"}
	}
	return []string{"--" + o.Name, "sample-value"}
}

// Each option of the table changes the options that the transfer reads.
// An option that the parser accepts and Apply then drops would do nothing
// without a sign; this test finds it.
func TestApply_EveryOptionChangesTheOptions(t *testing.T) {
	dir := t.TempDir()
	for i := range table {
		o := &table[i]
		if notInApply[o.Name] != "" || o.Type == typeTopic {
			continue
		}
		job := ""
		switch o.Scope {
		case "download", "mirror":
			job = o.Scope
		}
		base := []string{}
		if job != "" {
			base = []string{job}
		}
		want, err := Apply(apply(t, append(base, "http://h/x")...))
		if err != nil {
			t.Fatalf("%s: %v", job, err)
		}
		args := append(append(append([]string{}, base...), sampleValue(t, o, dir)...), "http://h/x")
		c, err := Parse(args)
		if err != nil {
			t.Errorf("--%s: parse %q: %v", o.Name, args, err)
			continue
		}
		got, err := Apply(c)
		if err != nil {
			if !strings.Contains(err.Error(), o.Name) {
				t.Errorf("--%s: %v", o.Name, err)
			}
			continue
		}
		if reflect.DeepEqual(want, got) {
			t.Errorf("--%s %v changed nothing", o.Name, args)
		}
	}
}
