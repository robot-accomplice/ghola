package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
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
	"show-headers":       func(o *config.Options, v string, _ *Command) error { o.Output.Include = v == "true"; return nil },
	"quiet":              func(o *config.Options, v string, _ *Command) error { o.Output.Silent = v == "true"; return nil },
	"progress":           setProgress,
	"verbose":            func(o *config.Options, v string, _ *Command) error { o.Output.Verbose = v == "true"; return nil },
	"har":                func(o *config.Options, v string, _ *Command) error { o.Output.HAR = v; return nil },
	"method":             func(o *config.Options, v string, _ *Command) error { o.Method = strings.ToUpper(v); return nil },
	"head":               setHead,
	"header":             func(o *config.Options, _ string, c *Command) error { o.Headers = c.Values["header"]; return nil },
	"user-agent": func(o *config.Options, v string, _ *Command) error {
		o.Stealth.Agent, o.Stealth.AgentExplicit = v, true
		return nil
	},
	"referer":      func(o *config.Options, v string, _ *Command) error { o.Stealth.Referer = v; return nil },
	"data":         func(o *config.Options, v string, _ *Command) error { o.Data = v; return nil },
	"data-literal": func(o *config.Options, v string, _ *Command) error { o.Data, o.DataLiteral = v, true; return nil },
	"retry-status": setRetryStatus,
	"field":        func(o *config.Options, _ string, c *Command) error { o.DataURLEncode = c.Values["field"]; return nil },
	"form":         func(o *config.Options, _ string, c *Command) error { o.Form = c.Values["form"]; return nil },
	"upload":       setUpload,
	"range":        func(o *config.Options, v string, _ *Command) error { o.Output.Range = v; return nil },
	"continue":     setContinue,
	"continue-at":  func(o *config.Options, v string, _ *Command) error { o.Output.ContinueAt = v; return nil },
	"chain":        func(o *config.Options, v string, _ *Command) error { o.Chain = v; return nil },
	"compressed":   func(o *config.Options, v string, _ *Command) error { o.Stealth.Compressed = v == "true"; return nil },
	"follow":       func(o *config.Options, v string, _ *Command) error { o.Resilience.Location = v == "true"; return nil },
	"follow-max":   setInt(func(o *config.Options) *int { return &o.Resilience.MaxRedirs }),
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
	"tls-verify":      func(o *config.Options, v string, _ *Command) error { o.Stealth.Insecure = v == "false"; return nil },
	"tls-ca":          func(o *config.Options, v string, _ *Command) error { o.Stealth.CACert = v; return nil },
	"tls-cert":        func(o *config.Options, v string, _ *Command) error { o.Stealth.ClientCert = v; return nil },
	"tls-key":         func(o *config.Options, v string, _ *Command) error { o.Stealth.ClientKey = v; return nil },
	"user":            func(o *config.Options, v string, _ *Command) error { o.User = v; return nil },
	"proxy":           func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.Proxy = v; return nil },
	"proxy-user":      func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.Auth = v; return nil },
	"proxy-file":      func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.File = v; return nil },
	"proxy-strategy":  func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.Strategy = v; return nil },
	"proxy-check":     func(o *config.Options, v string, _ *Command) error { o.ProxyCfg.HealthCheck = v == "true"; return nil },
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
	for name := range c.Values {
		if strings.HasPrefix(name, "proxy-tls-") {
			return nil, usagef("--%s is in the v1 spec but not built yet (phase 2)", name)
		}
	}
	config.TakeURLs(o, c.Args)
	applyDefaults(o, c)
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

func setInt(field func(*config.Options) *int) setter {
	return func(o *config.Options, v string, _ *Command) error {
		n, err := strconv.Atoi(v)
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
