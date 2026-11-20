package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/glob"
	"github.com/robot-accomplice/ghola/internal/netrc"
	"github.com/valyala/fasthttp"
)

// v1 defaults (docs/cli/v1-defaults.md, decided by Jon on 2026-11-18).
const (
	defaultFollowMax = 20
	defaultRetries   = 3
)

// idempotent methods retry by default (v1 defaults, item 6). A POST
// retries only with an explicit --retry-max: a retry repeats its effect.
var idempotent = map[string]bool{"GET": true, "HEAD": true, "PUT": true, "DELETE": true, "OPTIONS": true}

// setter writes one option into config.Options. v is the last value; c
// gives the other values of a repeated option.
type setter func(o *config.Options, v string, c *Command) error

// setters holds each option that this build carries out. An option of the
// table without a setter belongs to a phase that is not built yet, and
// Apply refuses it with that phase, so no option is accepted and ignored.
var setters = map[string]setter{
	"output":             setOutput,
	"output-name":        func(o *config.Options, v string, _ *Command) error { o.Output.RemoteHeader = v == "header"; return nil },
	"output-remote-time": func(o *config.Options, v string, _ *Command) error { o.Output.RemoteTime = v == "true"; return nil },
	"output-select":      func(o *config.Options, v string, _ *Command) error { o.Output.JQ = v; return nil },
	"cond-time":          func(*config.Options, string, *Command) error { return nil }, // read in Apply
	"url-path-as-is":     func(o *config.Options, v string, _ *Command) error { o.PathAsIs = v == "true"; return nil },
	"request-target":     func(o *config.Options, v string, _ *Command) error { o.RequestTarget = v; return nil },
	"config":             func(*config.Options, string, *Command) error { return nil }, // read in WithConfig
	"no-config":          func(*config.Options, string, *Command) error { return nil }, // read in WithConfig
	"cond-etag-save":     func(o *config.Options, v string, _ *Command) error { o.Output.ETagSave = v; return nil },
	"cond-etag-compare":  func(*config.Options, string, *Command) error { return nil }, // read in Apply
	"interface":          func(o *config.Options, v string, _ *Command) error { o.Net.Interface = v; return nil },
	"local-port":         func(o *config.Options, v string, _ *Command) error { o.Net.LocalPort = v; return nil },
	"unix-socket":        func(o *config.Options, v string, _ *Command) error { o.Net.UnixSocket = v; return nil },
	"tls-ciphers": func(o *config.Options, _ string, c *Command) error {
		o.Stealth.Ciphers = c.Values["tls-ciphers"]
		return nil
	},
	"tls-crl":       func(o *config.Options, v string, _ *Command) error { o.Stealth.CRL = v; return nil },
	"tls-ocsp":      func(o *config.Options, v string, _ *Command) error { o.Stealth.OCSP = v == "true"; return nil },
	"tls-cert-type": func(o *config.Options, v string, _ *Command) error { o.Stealth.CertType = v; return nil },
	"auth":          setAuth,
	"output-template": func(o *config.Options, v string, _ *Command) error {
		t, err := config.ParseTemplate(v)
		o.Output.Template = t
		return err
	},
	"hsts":           func(o *config.Options, v string, _ *Command) error { o.HSTS = v; return nil },
	"auth-aws-sigv4": func(*config.Options, string, *Command) error { return nil }, // read in Apply
	"trace":          func(o *config.Options, v string, _ *Command) error { o.Output.Trace = v; return nil },
	"trace-format":   func(o *config.Options, v string, _ *Command) error { o.Output.TraceASCII = v == "ascii"; return nil },
	"trace-time":     func(o *config.Options, v string, _ *Command) error { o.Output.TraceTime = v == "true"; return nil },
	"doh":            func(o *config.Options, v string, _ *Command) error { o.Net.DoH = v; return nil },
	"save-headers":   func(o *config.Options, v string, _ *Command) error { o.Output.SaveHeaders = v; return nil },
	"show-headers":   func(o *config.Options, v string, _ *Command) error { o.Output.Include = v == "true"; return nil },
	"quiet":          func(o *config.Options, v string, _ *Command) error { o.Output.Silent = v == "true"; return nil },
	"progress":       setProgress,
	"verbose":        func(o *config.Options, v string, _ *Command) error { o.Output.Verbose = v == "true"; return nil },
	"har":            func(o *config.Options, v string, _ *Command) error { o.Output.HAR = v; return nil },
	"method":         func(o *config.Options, v string, _ *Command) error { o.Method = strings.ToUpper(v); return nil },
	"head":           setHead,
	"header":         func(o *config.Options, _ string, c *Command) error { o.Headers = c.Values["header"]; return nil },
	"user-agent": func(o *config.Options, v string, _ *Command) error {
		o.Stealth.Agent, o.Stealth.AgentExplicit = v, true
		return nil
	},
	"referer":            func(o *config.Options, v string, _ *Command) error { o.Stealth.Referer = v; return nil },
	"data":               func(o *config.Options, v string, _ *Command) error { o.Data = v; return nil },
	"data-literal":       func(o *config.Options, v string, _ *Command) error { o.Data, o.DataLiteral = v, true; return nil },
	"retry-status":       setRetryStatus,
	"field":              func(o *config.Options, _ string, c *Command) error { o.DataURLEncode = c.Values["field"]; return nil },
	"form":               func(o *config.Options, _ string, c *Command) error { o.Form = c.Values["form"]; return nil },
	"upload":             setUpload,
	"json":               setJSON,
	"output-create-dirs": func(o *config.Options, v string, _ *Command) error { o.Output.CreateDirs = v == "true"; return nil },
	"output-overwrite":   func(o *config.Options, v string, _ *Command) error { o.Output.NoOverwrite = v == "false"; return nil },
	"max-size":           setMaxSize,
	"url-glob":           func(*config.Options, string, *Command) error { return nil }, // read in Apply
	"auth-netrc":         func(*config.Options, string, *Command) error { return nil }, // read in Apply
	"auth-netrc-file":    func(*config.Options, string, *Command) error { return nil }, // read in Apply
	"ip": func(o *config.Options, v string, _ *Command) error {
		o.Net.IPVersion = strings.TrimPrefix(v, "any")
		return nil
	},
	"resolve": func(o *config.Options, _ string, c *Command) error { o.Net.Resolve = c.Values["resolve"]; return nil },
	"connect-to": func(o *config.Options, _ string, c *Command) error {
		o.Net.ConnectTo = c.Values["connect-to"]
		return nil
	},
	"auth-bearer": setBearer,
	"query":       func(*config.Options, string, *Command) error { return nil }, // applied to the URLs in Apply
	"range":       func(o *config.Options, v string, _ *Command) error { o.Output.Range = v; return nil },
	"continue":    setContinue,
	"continue-at": func(o *config.Options, v string, _ *Command) error { o.Output.ContinueAt = v; return nil },
	"chain":       func(o *config.Options, v string, _ *Command) error { o.Chain = v; return nil },
	"compressed":  func(o *config.Options, v string, _ *Command) error { o.Stealth.Compressed = v == "true"; return nil },
	"follow":      func(o *config.Options, v string, _ *Command) error { o.Resilience.Location = v == "true"; return nil },
	"follow-max":  setInt(func(o *config.Options) *int { return &o.Resilience.MaxRedirs }),
	"follow-trusted": func(o *config.Options, v string, _ *Command) error {
		o.Resilience.LocationTrusted = v == "true"
		return nil
	},
	"fail":            func(o *config.Options, v string, _ *Command) error { o.Output.Fail = v == "true"; return nil },
	"max-header-size": setMaxHeader,
	"timeout":         setTimeout,
	"timeout-connect": setConnectTimeout,
	"stall-time":      setStallTime,
	"stall-rate":      setStallRate,
	"rate-max":        setRateMax,
	"wait-jitter":     setWaitJitter,
	"retry-max":       setInt(func(o *config.Options) *int { return &o.Resilience.Retries }),
	"retry-delay":     setRetryDelay,
	"retry-max-time": func(o *config.Options, v string, _ *Command) error {
		d, err := ParseDuration(v)
		o.Resilience.RetryMaxTime = d
		return err
	},
	"tls-verify":     func(o *config.Options, v string, _ *Command) error { o.Stealth.Insecure = v == "false"; return nil },
	"tls-ca":         func(o *config.Options, v string, _ *Command) error { o.Stealth.CACert = v; return nil },
	"tls-cert":       func(o *config.Options, v string, _ *Command) error { o.Stealth.ClientCert = v; return nil },
	"tls-key":        func(o *config.Options, v string, _ *Command) error { o.Stealth.ClientKey = v; return nil },
	"tls-min":        func(o *config.Options, v string, _ *Command) error { o.Stealth.TLSMin = v; return nil },
	"tls-max":        func(o *config.Options, v string, _ *Command) error { o.Stealth.TLSMax = v; return nil },
	"tls-ca-dir":     func(o *config.Options, v string, _ *Command) error { o.Stealth.CADir = v; return nil },
	"tls-pin":        func(o *config.Options, _ string, c *Command) error { o.Stealth.Pins = c.Values["tls-pin"]; return nil },
	"user":           func(o *config.Options, v string, _ *Command) error { o.User = v; return nil },
	"proxy":          func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.Proxy = v; return nil },
	"proxy-user":     func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.Auth = v; return nil },
	"proxy-file":     func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.File = v; return nil },
	"proxy-strategy": func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.Strategy = v; return nil },
	"proxy-bypass":   func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.Bypass = &v; return nil },
	"proxy-check":    func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.HealthCheck = v == "true"; return nil },
	"cookie": func(o *config.Options, _ string, c *Command) error {
		o.Stealth.Cookies = c.Values["cookie"]
		return nil
	},
	"cookie-jar":  func(o *config.Options, v string, _ *Command) error { o.Stealth.CookieJar = v; return nil },
	"impersonate": func(o *config.Options, v string, _ *Command) error { o.Stealth.Impersonate = v; return nil },
	"impersonate-headers": func(o *config.Options, v string, _ *Command) error {
		o.Stealth.StealthHeaders = v == "true"
		return nil
	},
	"impersonate-accept-language": func(o *config.Options, v string, _ *Command) error { o.Stealth.AcceptLanguage = v; return nil },
	"connections":                 setInt(func(o *config.Options) *int { return &o.Concurrency }),
	"http":                        setHTTP,
}

// handled by the caller of Apply, not by a setter.
var callerOptions = map[string]bool{"help": true, "version": true}

// Apply turns a parsed transfer command (fetch or download) into the
// options of the transfer code, with the v1 defaults.
func Apply(c *Command) (*config.Options, error) {
	o := &config.Options{
		Method: fasthttp.MethodGet, Concurrency: 1,
		Stealth:    config.StealthOptions{Agent: "ghola/" + config.Version, Referer: "none", Compressed: true},
		Resilience: config.ResilienceOptions{Location: true, MaxRedirs: defaultFollowMax, Backoff: config.DefaultBackoffMs, BufferSize: config.DefaultBufferSize},
		Output:     config.OutputOptions{Fail: true, FailFrom400: true},
		ProxyCfg:   config.ProxyOptions{Strategy: "sticky"},
	}
	for i := range table {
		name := table[i].Name
		if !c.Has(name) || callerOptions[name] {
			continue
		}
		set, ok := setters[name]
		if !ok {
			return nil, usagef("--%s is in the v1 spec but not built yet (phase %s)", name, table[i].Phase)
		}
		if err := set(o, c.Last(name), c); err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
	}
	if err := applyProxyTLS(o, c); err != nil {
		return nil, err
	}
	urls, err := expandURLs(c.Args, c.Last("url-glob") != "false")
	if err != nil {
		return nil, err
	}
	config.TakeURLs(o, urls)
	if err := applyNetrc(o, c); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	applyCondTime(o, c.Last("cond-time"), os.Stderr)
	if v := c.Last("auth-aws-sigv4"); v != "" {
		if o.SigV4, err = config.ParseSigV4(v, o.URL); err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
	}
	if err := applyETagCompare(o, c.Last("cond-etag-compare")); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	if err := addQuery(o, c.Values["query"]); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	applyDefaults(o, c)
	if c.Job == JobDownload && !c.Has("output-overwrite") {
		o.Output.NoOverwrite = true // ghola download keeps an existing file (the spec default)
	}
	if c.Job == JobDownload && o.Output.File == "" && !o.Output.Stdout {
		o.Output.RemoteName = true // ghola download saves under the remote name
	}
	explicit := map[string]bool{config.ExplicitMethod: c.Has("method") || c.Has("head") || c.Has("upload"), config.ExplicitProfileHeaders: c.Has("impersonate-headers")}
	if err := config.Finish(o, func(name string) bool { return explicit[name] }); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	return o, nil
}

// applyDefaults sets the v1 defaults that depend on other options.
func applyDefaults(o *config.Options, c *Command) {
	byteRange := o.Output.ContinueAt != "" || o.Output.Range != "" || o.Concurrency > 1
	if byteRange && !c.Has("compressed") {
		o.Stealth.Compressed = false // byte ranges apply to the encoded body: ask for identity
	}
	method := o.Method
	if !c.Has("method") && (o.Data != "" || len(o.Form) > 0 || len(o.DataURLEncode) > 0) {
		method = fasthttp.MethodPost
	}
	if !c.Has("retry-max") && idempotent[method] {
		o.Resilience.Retries = defaultRetries
	}
	if o.Resilience.Retries > 0 {
		o.Resilience.RetryHTTP = true // retry the transient status codes too, as curl --retry does
	}
	if len(o.Resilience.RetryStatus) == 0 {
		o.Resilience.RetryStatus = defaultRetryStatus()
	}
}

// defaultRetryStatus is the v1 default of --retry-status (v1 defaults,
// item 6), from the option table.
func defaultRetryStatus() []int {
	list, _ := statusList(byName["retry-status"].Default)
	return list
}

func setRetryStatus(o *config.Options, v string, _ *Command) error {
	list, err := statusList(v)
	o.Resilience.RetryStatus = list
	return err
}

// statusList reads "429,503" as status codes.
func statusList(v string) ([]int, error) {
	var out []int
	for _, f := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 100 || n > 599 {
			return nil, fmt.Errorf("--retry-status: %q is not a status code", f)
		}
		out = append(out, n)
	}
	return out, nil
}

// setInt stores a count. A count cannot be negative: before, -r -1 crashed
// the bridge (audit finding H5).
func setInt(field func(*config.Options) *int) setter {
	return func(o *config.Options, v string, _ *Command) error {
		n, err := strconv.Atoi(v)
		if err == nil && n < 0 {
			return fmt.Errorf("%s cannot be negative", v)
		}
		*field(o) = n
		return err
	}
}

// setOutput handles -o FILE, -o - (stdout) and -o DIR/ (the remote name
// inside the folder).
func setOutput(o *config.Options, v string, _ *Command) error {
	if v == "-" {
		o.Output.Stdout = true
		return nil
	}
	if info, err := os.Stat(v); strings.HasSuffix(v, "/") || strings.HasSuffix(v, string(filepath.Separator)) || (err == nil && info.IsDir()) {
		o.Output.RemoteName = true
		o.Output.Dir = v
		return nil
	}
	o.Output.File = v
	return nil
}

func setProgress(o *config.Options, v string, _ *Command) error {
	o.Output.Progress = map[string]string{"true": "on", "false": "off"}[v]
	return nil
}

// expandURLs expands curl's URL globs ({a,b}, [1-9]) unless
// --no-url-glob. A bad glob is a *glob.Error, which exits 3, as in curl.
func expandURLs(args []string, globbing bool) ([]string, error) {
	if !globbing {
		return args, nil
	}
	var out []string
	for _, a := range args {
		p, err := glob.Parse(a)
		if err != nil {
			return nil, err
		}
		p.Each(func(u string, _ []string) bool { out = append(out, u); return true })
	}
	return out, nil
}

// applyNetrc reads the credentials for the first URL's host from the
// netrc file (--auth-netrc reads ~/.netrc, --auth-netrc-file a given
// file), unless -u gives them. A user in the URL picks its entry. The
// credentials go only to that host, as -u does (audit finding H1).
func applyNetrc(o *config.Options, c *Command) error {
	path := c.Last("auth-netrc-file")
	if path == "" && c.Last("auth-netrc") != "true" {
		return nil
	}
	if o.User != "" || o.URL == "" {
		return nil
	}
	explicit := path != ""
	if !explicit {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		path = filepath.Join(home, ".netrc")
	}
	entries, err := netrc.ParseFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return nil // no ~/.netrc: no credentials, as curl -n, wget and aria2c do
	}
	if err != nil {
		return fmt.Errorf("--auth-netrc: %w", err)
	}
	u, err := url.Parse(o.URL)
	if err != nil {
		return nil
	}
	if e, ok := netrc.Find(entries, u.Hostname(), u.User.Username()); ok {
		o.User = e.Login + ":" + e.Password
	}
	return nil
}

// condTimeFormats are the date forms of --cond-time, besides the HTTP
// date. curl reads more forms; these are the common ones.
var condTimeFormats = []string{time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}

// applyCondTime adds the --cond-time condition: If-Modified-Since the time
// of a file or a date, or If-Unmodified-Since with a leading "-". As in
// curl -z, a value that is not a file or a date sends no condition, with
// a warning: on the first run of "-z f -o f", f does not exist yet. A
// header that -H sets wins.
func applyCondTime(o *config.Options, value string, warn io.Writer) {
	if value == "" {
		return
	}
	name := "If-Modified-Since"
	if rest, ok := strings.CutPrefix(value, "-"); ok {
		name, value = "If-Unmodified-Since", rest
	}
	t, ok := condTime(value)
	if !ok {
		if !o.Output.Silent {
			fmt.Fprintf(warn, "Warning: --cond-time: %q is not a file or a date; the request has no condition\n", value)
		}
		return
	}
	if hasHeader(o.Headers, name) {
		return
	}
	o.Headers = append(o.Headers, name+": "+t.UTC().Format(http.TimeFormat))
	o.Output.CondTime = true
}

func condTime(value string) (time.Time, bool) {
	if fi, err := os.Stat(value); err == nil {
		return fi.ModTime(), true
	}
	for _, f := range condTimeFormats {
		if t, err := time.Parse(f, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// applyETagCompare sends If-None-Match with the ETag in the
// --cond-etag-compare file, as curl --etag-compare does. A file that does
// not exist yet, or an empty one, sends no condition: on the first run of
// "--cond-etag-compare f --cond-etag-save f", f does not exist. A header
// that -H sets wins.
func applyETagCompare(o *config.Options, path string) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("--cond-etag-compare: %w", err)
	}
	etag, _, _ := strings.Cut(string(b), "\n")
	if etag = strings.TrimSpace(etag); etag == "" || hasHeader(o.Headers, "If-None-Match") {
		return nil
	}
	o.Headers = append(o.Headers, "If-None-Match: "+etag)
	o.Output.CondTime = true
	return nil
}

// hasHeader reports a header that -H sets.
func hasHeader(headers []string, name string) bool {
	for _, h := range headers {
		if k, _, _ := strings.Cut(h, ":"); strings.EqualFold(strings.TrimSpace(k), name) {
			return true
		}
	}
	return false
}

// setAuth reads --auth. NTLM and Negotiate need the system's SSPI or
// Kerberos, which ghola does not reach yet; they are refused, not dropped.
func setAuth(o *config.Options, v string, _ *Command) error {
	switch v {
	case "basic", "digest", "any":
		o.Auth = v
		return nil
	case "ntlm", "negotiate":
		return fmt.Errorf("--auth %s is not built yet: it needs the system's SSPI or Kerberos; use basic, digest or any", v)
	}
	return fmt.Errorf("--auth %q: use basic, digest, any, ntlm or negotiate", v)
}

// applyProxyTLS runs the --tls-* setter of each --proxy-tls-* option on
// the proxy's own TLS settings, so the two share their rules.
func applyProxyTLS(o *config.Options, c *Command) error {
	for name, values := range c.Values {
		base, ok := strings.CutPrefix(name, "proxy-")
		if !ok || !strings.HasPrefix(base, "tls-") {
			continue
		}
		set, ok := setters[base]
		if !ok {
			return usagef("--%s is in the v1 spec but not built yet (phase 2)", name)
		}
		proxy := &config.Options{Stealth: o.ProxyCfg.TLS}
		if err := set(proxy, values[len(values)-1], &Command{Values: map[string][]string{base: values}}); err != nil {
			return &UsageError{Msg: fmt.Sprintf("--%s: %v", name, err)}
		}
		o.ProxyCfg.TLS = proxy.Stealth
	}
	return nil
}

// setJSON sends a JSON body: Content-Type and Accept are
// application/json unless -H sets them, as curl --json does.
func setJSON(o *config.Options, v string, _ *Command) error {
	o.Data = v
	for _, h := range []string{"Content-Type", "Accept"} {
		if !headerSet(o.Headers, h) {
			o.Headers = append(o.Headers, h+": application/json")
		}
	}
	return nil
}

// setBearer sends the token as Authorization: Bearer. It is a credential
// header, so it goes only to the host of the first URL unless
// --follow-trusted (audit finding H1).
func setBearer(o *config.Options, v string, _ *Command) error {
	o.Headers = append(o.Headers, "Authorization: Bearer "+v)
	return nil
}

func headerSet(headers []string, name string) bool {
	for _, h := range headers {
		if k, _, ok := config.ParseHeader(h); ok && strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// addQuery adds each --query name=value to every URL, URL-encoded, as
// curl --url-query does.
func addQuery(o *config.Options, params []string) error {
	if len(params) == 0 {
		return nil
	}
	enc := make([]string, 0, len(params))
	for _, p := range params {
		name, value, ok := strings.Cut(p, "=")
		if !ok || name == "" {
			return fmt.Errorf("--query %q: use name=value", p)
		}
		enc = append(enc, url.QueryEscape(name)+"="+url.QueryEscape(value))
	}
	for i, u := range o.URLs {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		o.URLs[i] = u + sep + strings.Join(enc, "&")
	}
	o.URL = o.URLs[0]
	return nil
}

func setMaxSize(o *config.Options, v string, _ *Command) error {
	n, err := ParseSize(v)
	o.Output.MaxSize = n
	return err
}

func setHead(o *config.Options, v string, _ *Command) error {
	if v == "true" {
		o.Method, o.Output.Include = fasthttp.MethodHead, true
	}
	return nil
}

func setUpload(o *config.Options, v string, _ *Command) error {
	o.Transfer, o.Method = v, fasthttp.MethodPut
	return nil
}

func setContinue(o *config.Options, v string, _ *Command) error {
	if v == "true" {
		o.Output.ContinueAt = "-"
	}
	return nil
}

func setMaxHeader(o *config.Options, v string, _ *Command) error {
	n, err := ParseSize(v)
	o.Resilience.BufferSize = int(n)
	return err
}

func setTimeout(o *config.Options, v string, _ *Command) error {
	d, err := ParseDuration(v)
	o.Resilience.Timeout = int(d.Milliseconds())
	return err
}

func setConnectTimeout(o *config.Options, v string, _ *Command) error {
	d, err := ParseDuration(v)
	o.Resilience.ConnectTimeout = d
	return err
}

func setStallTime(o *config.Options, v string, _ *Command) error {
	d, err := ParseDuration(v)
	o.Resilience.SpeedTime = d
	if o.Resilience.SpeedLimit == 0 {
		o.Resilience.SpeedLimit = 1 // curl's default with --speed-time alone
	}
	return err
}

func setStallRate(o *config.Options, v string, _ *Command) error {
	n, err := ParseRate(v)
	o.Resilience.SpeedLimit = n
	if o.Resilience.SpeedTime == 0 {
		o.Resilience.SpeedTime = config.DefaultSpeedTime
	}
	return err
}

// setRateMax stores the rate in bytes, so the v0 rate parser (k = 1000)
// reads the v1 value (K = 1024) unchanged.
func setRateMax(o *config.Options, v string, _ *Command) error {
	n, err := ParseRate(v)
	o.Output.LimitRate = strconv.FormatInt(n, 10)
	return err
}

func setWaitJitter(o *config.Options, v string, _ *Command) error {
	d, err := ParseDuration(v)
	o.Stealth.Drift = int(d.Milliseconds())
	return err
}

func setRetryDelay(o *config.Options, v string, _ *Command) error {
	d, err := ParseDuration(v)
	o.Resilience.Backoff = int(d.Milliseconds())
	return err
}

// setHTTP: only --http 3 (with --impersonate) is built; the other versions
// come in phase 2.
func setHTTP(o *config.Options, v string, _ *Command) error {
	switch v {
	case "auto":
		return nil
	case "3":
		o.Stealth.HTTP3 = true
		return nil
	}
	return fmt.Errorf("--http %s is in the v1 spec but not built yet (phase 2)", v)
}
