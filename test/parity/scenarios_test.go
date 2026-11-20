//go:build parity

package parity

// Each scenario runs the same job on each tool that can do it, and checks the
// result against one rule for all tools. The rule states the correct result,
// not the behaviour of one tool, so a reference tool can fail it too. The
// Finding field ties a scenario to the audit finding it reproduces
// (docs/audits/2026-11-12-v0.7.0-vs-curl-aria2-wget.md), or to "ok" for
// behaviour that already works and must not regress.

type scenario struct {
	ID      string
	Finding string
	What    string
	Runs    map[string]func(e *Env) Spec
	Check   check
}

func args(a ...string) Spec { return Spec{Args: a} }

const seedLen = 300 << 10 // the size of the partial file in resume scenarios

func seeded(size string, mk func(e *Env) Spec) func(e *Env) Spec {
	return func(e *Env) Spec {
		s := mk(e)
		s.Seed = e.Ref(size)[:seedLen]
		return s
	}
}

func withEnv(env []string, mk func(e *Env) Spec) func(e *Env) Spec {
	return func(e *Env) Spec { s := mk(e); s.Env = append(s.Env, env...); return s }
}

// toFileArgs is each tool's "quiet, write the body to the file f" arguments.
var toFileArgs = map[string][]string{
	"ghola": {"-o", "f"},
	"curl":  {"-sS", "-o", "f"},
	"wget":  {"-q", "-O", "f"},
	"aria2": {"-q", "--allow-overwrite=true", "-o", "f"},
}

// toFile is the command that makes tool download url to the file f. Scenarios
// that do not test stdout use it, so a stdout defect cannot hide their result.
func toFile(tool, url string, extra ...string) Spec {
	a := append(append(append([]string(nil), extra...), toFileArgs[tool]...), url)
	return Spec{Args: a}
}

// downloadTo is toFile on path for each tool, with extra arguments per tool.
func downloadTo(path string, extra map[string][]string) map[string]func(e *Env) Spec {
	runs := map[string]func(e *Env) Spec{}
	for name := range toFileArgs {
		runs[name] = func(e *Env) Spec { return toFile(name, e.URL(path), extra[name]...) }
	}
	return runs
}

// resumeFrom is the "resume the file f from URL" command of each tool.
func resumeFrom(path string) map[string]func(e *Env) Spec {
	return map[string]func(e *Env) Spec{
		"ghola": seeded("1m", func(e *Env) Spec { return args("-C", "-", "-o", "f", e.URL(path)) }),
		"curl":  seeded("1m", func(e *Env) Spec { return args("-sS", "-C", "-", "-o", "f", e.URL(path)) }),
		"wget":  seeded("1m", func(e *Env) Spec { return args("-q", "-c", "-O", "f", e.URL(path)) }),
		"aria2": seeded("1m", func(e *Env) Spec { return args("-q", "-c", "--allow-overwrite=true", "-o", "f", e.URL(path)) }),
	}
}

// resumeChanged resumes a partial file of /changing. For ghola the partial
// file is what an earlier, interrupted ghola download left: the first bytes
// of version 0 and the state file that names version 0 (ETag "v0"). The
// other tools keep no record of the version, so they get the plain seed.
func resumeChanged(path string) map[string]func(e *Env) Spec {
	runs := resumeFrom(path)
	runs["ghola"] = func(e *Env) Spec {
		s := args("-C", "-", "-o", "f", e.URL(path))
		s.Seed = changingVersion(e.s, parseSize("1m"), 0)[:seedLen]
		s.SeedFiles = map[string][]byte{"f.ghola": []byte(`{"url":"` + e.URL(path) + `","etag":"\"v0\""}`)}
		return s
	}
	return runs
}

// segmented is the "download URL in 4 parts" command of the tools that can.
func segmented(path string) map[string]func(e *Env) Spec {
	return map[string]func(e *Env) Spec{
		"ghola": func(e *Env) Spec { return args("-n", "4", "-o", "f", e.URL(path)) },
		"aria2": func(e *Env) Spec {
			return args("-q", "-x", "4", "-s", "4", "-k", "1M", "--allow-overwrite=true", "-o", "f", e.URL(path))
		},
	}
}

var scenarios = []scenario{
	// ---- Downloads that work today (must not regress) ----
	{
		ID: "get_1m", Finding: "ok", What: "download 1 MiB to a file",
		Runs:  downloadTo("/file/1m", nil),
		Check: all(exitZero, fileIs("f", ref("1m"))),
	},
	{
		ID: "truncated", Finding: "ok", What: "a body cut off before Content-Length is an error",
		Runs:  downloadTo("/truncate/1m", map[string][]string{"wget": {"-t", "1"}, "aria2": {"--max-tries=1"}}),
		Check: exitNonzero,
	},
	{
		ID: "rate_file", Finding: "ok", What: "64 KiB/s rate limit on a 256 KiB file takes about 4 s",
		Runs: downloadTo("/file/256k", map[string][]string{
			"ghola": {"--limit-rate", "64k"}, "curl": {"--limit-rate", "64k"},
			"wget": {"--limit-rate=64k"}, "aria2": {"--max-download-limit=64K"},
		}),
		Check: all(exitZero, slowerThan(3000), fileIs("f", ref("256k"))),
	},
	{
		ID: "no_head", Finding: "M5", What: "a download sends no extra HEAD request",
		Runs:  downloadTo("/file/1k", nil),
		Check: all(exitZero, requestsAre("HEAD", 0)),
	},
	{
		ID: "progress_not_tty", Finding: "M4", What: "no progress meter when stderr is not a terminal",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-o", "f", e.URL("/file/1m")) },
			"curl":  func(e *Env) Spec { return args("-o", "f", e.URL("/file/1m")) },
			"wget":  func(e *Env) Spec { return args("-O", "f", e.URL("/file/1m")) },
			"aria2": func(e *Env) Spec { return args("--allow-overwrite=true", "-o", "f", e.URL("/file/1m")) },
		},
		Check: all(exitZero, noMeter),
	},
	{
		ID: "progress_total", Finding: "M4", What: "progress never shows more than 100%",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--compressed", "-o", "f", e.URL("/gzip")) },
			"curl":  func(e *Env) Spec { return args("--compressed", "-o", "f", e.URL("/gzip")) },
		},
		Check: all(exitZero, percentAtMost100),
	},

	// ---- Byte-exact stdout ----
	{
		ID: "stdout_binary", Finding: "H2", What: "a binary body on stdout is byte-exact",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args(e.URL("/file/1k")) },
			"curl":  func(e *Env) Spec { return args("-sS", e.URL("/file/1k")) },
			"wget":  func(e *Env) Spec { return args("-q", "-O", "-", e.URL("/file/1k")) },
		},
		Check: all(exitZero, stdoutIs(ref("1k"))),
	},
	{
		ID: "stdout_gzip", Finding: "H2", What: "a decoded gzip body on stdout is byte-exact",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--compressed", e.URL("/gzip")) },
			"curl":  func(e *Env) Spec { return args("-sS", "--compressed", e.URL("/gzip")) },
		},
		Check: all(exitZero, stdoutIs(text(gzipText))),
	},
	{
		ID: "stdout_verbose", Finding: "H2", What: "-v keeps its lines off stdout",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-v", e.URL("/file/1k")) },
			"curl":  func(e *Env) Spec { return args("-sSv", e.URL("/file/1k")) },
		},
		Check: all(exitZero, stdoutIs(ref("1k"))),
	},
	{
		ID: "stdout_memory", Finding: "M2", What: "a 128 MiB body on stdout streams in under 64 MiB of memory",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args(e.URL("/file/128m")) },
			"curl":  func(e *Env) Spec { return args("-sS", e.URL("/file/128m")) },
			"wget":  func(e *Env) Spec { return args("-q", "-O", "-", e.URL("/file/128m")) },
		},
		Check: all(exitZero, stdoutIs(ref("128m")), peakBelow(64<<20)),
	},
	{
		ID: "rate_stdout", Finding: "M3", What: "the rate limit applies to stdout too",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--limit-rate", "64k", e.URL("/file/256k")) },
			"curl":  func(e *Env) Spec { return args("-sS", "--limit-rate", "64k", e.URL("/file/256k")) },
			"wget":  func(e *Env) Spec { return args("-q", "--limit-rate=64k", "-O", "-", e.URL("/file/256k")) },
		},
		Check: all(exitZero, slowerThan(3000), stdoutIs(ref("256k"))),
	},

	// ---- Segmented downloads and resume (audit C2 and C3, fixed in v0.8.0) ----
	{ID: "seg_ok", Finding: "ok", What: "4-part download", Runs: segmented("/file/8m"), Check: all(exitZero, fileIs("f", ref("8m")))},
	{ID: "seg_norange", Finding: "ok", What: "4-part download from a server without Range", Runs: segmented("/norange/8m"), Check: all(exitZero, fileIs("f", ref("8m")))},
	{ID: "seg_liar", Finding: "C2", What: "4-part download from a server that ignores Range", Runs: segmented("/liar/8m"), Check: segmentedOK("8m")},
	{ID: "seg_badrange", Finding: "C2", What: "4-part download with a wrong Content-Range", Runs: segmented("/badrange/8m"), Check: segmentedOK("8m")},
	{ID: "resume_ok", Finding: "ok", What: "resume a 300 KiB partial file", Runs: resumeFrom("/file/1m"), Check: all(exitZero, fileIs("f", ref("1m")))},
	{ID: "resume_liar", Finding: "C3", What: "resume from a server that ignores Range", Runs: resumeFrom("/liar/1m"), Check: resumeOK("1m")},
	{ID: "resume_badrange", Finding: "C3", What: "resume with a wrong Content-Range", Runs: resumeFrom("/badrange/1m"), Check: resumeOK("1m")},
	{ID: "resume_changed", Finding: "GAP-if-range", What: "resume when the remote file changed", Runs: resumeChanged("/changing/1m"), Check: changedOK("1m")},

	// ---- Redirects and credentials ----
	{
		ID: "redirect_follow", Finding: "ok", What: "follow 3 redirects",
		Runs:  downloadTo("/redirect/3", map[string][]string{"ghola": {"-L"}, "curl": {"-L"}}),
		Check: all(exitZero, fileIs("f", ref("1k"))),
	},
	{
		ID: "redirect_limit", Finding: "H6", What: "a 60-hop redirect chain is an error",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-L", e.URL("/redirect/60")) },
			"curl":  func(e *Env) Spec { return args("-sSL", e.URL("/redirect/60")) },
			"wget":  func(e *Env) Spec { return args("-q", "-O", "-", e.URL("/redirect/60")) },
			"aria2": func(e *Env) Spec { return args("-q", "--allow-overwrite=true", "-o", "f", e.URL("/redirect/60")) },
		},
		Check: exitNonzero,
	},
	{
		ID: "xhost_basic", Finding: "H1", What: "-u credentials do not follow a redirect to another host",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-L", "-u", "alice:s3cret", e.URL("/xhost")) },
			"curl":  func(e *Env) Spec { return args("-sSL", "-u", "alice:s3cret", e.URL("/xhost")) },
			"wget": func(e *Env) Spec {
				return args("-q", "--user", "alice", "--password", "s3cret", "--auth-no-challenge", "-O", "-", e.URL("/xhost"))
			},
			"aria2": func(e *Env) Spec {
				return args("-q", "--http-user=alice", "--http-passwd=s3cret", "--allow-overwrite=true", "-o", "f", e.URL("/xhost"))
			},
		},
		Check: lastTo("/echo", "localhost", headerAbsent("Authorization")),
	},
	{
		ID: "xhost_bearer", Finding: "H1", What: "an Authorization header does not follow a redirect to another host",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-L", "-H", "Authorization: Bearer tok", e.URL("/xhost")) },
			"curl":  func(e *Env) Spec { return args("-sSL", "-H", "Authorization: Bearer tok", e.URL("/xhost")) },
			"wget": func(e *Env) Spec {
				return args("-q", "--header", "Authorization: Bearer tok", "-O", "-", e.URL("/xhost"))
			},
			"aria2": func(e *Env) Spec {
				return args("-q", "--header=Authorization: Bearer tok", "--allow-overwrite=true", "-o", "f", e.URL("/xhost"))
			},
		},
		Check: lastTo("/echo", "localhost", headerAbsent("Authorization")),
	},
	{
		ID: "xhost_cookie", Finding: "H1", What: "a cookie given on the command line does not follow a redirect to another host",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-L", "--cookie", "sid=abc", e.URL("/xhost")) },
			"curl":  func(e *Env) Spec { return args("-sSL", "-b", "sid=abc", e.URL("/xhost")) },
		},
		Check: lastTo("/echo", "localhost", headerAbsent("Cookie")),
	},
	{
		ID: "xhost_trusted", Finding: "H1", What: "--location-trusted sends the credentials to the other host",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec {
				return args("--location-trusted", "-H", "Authorization: Bearer tok", e.URL("/xhost"))
			},
			"curl": func(e *Env) Spec {
				return args("-sS", "--location-trusted", "-H", "Authorization: Bearer tok", e.URL("/xhost"))
			},
		},
		Check: lastTo("/echo", "localhost", headerIs("Authorization", "Bearer tok")),
	},
	{
		ID: "r303", Finding: "ok", What: "a 303 after a POST becomes a GET",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-L", "-d", "hello", e.URL("/r303")) },
			"curl":  func(e *Env) Spec { return args("-sSL", "-d", "hello", e.URL("/r303")) },
		},
		Check: lastTo("/echo", "", methodBody("GET", "")),
	},
	{
		ID: "r307", Finding: "ok", What: "a 307 keeps the POST and its body",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-L", "-d", "hello", e.URL("/r307")) },
			"curl":  func(e *Env) Spec { return args("-sSL", "-d", "hello", e.URL("/r307")) },
		},
		Check: lastTo("/echo", "", methodBody("POST", "hello")),
	},

	// ---- Timeouts ----
	{
		ID: "timeout_stdout", Finding: "ok", What: "a 2 s timeout stops a hung request",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--timeout", "2000", e.URL("/hang")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-m", "2", e.URL("/hang")) },
		},
		Check: all(exitNonzero, fasterThan(10000)),
	},
	{
		ID: "timeout_file", Finding: "H3", What: "a 2 s timeout stops a hung download to a file",
		Runs: downloadTo("/hang", map[string][]string{
			"ghola": {"--timeout", "2000"}, "curl": {"-m", "2"},
			"wget": {"-t", "1", "-T", "2"}, "aria2": {"--max-tries=1", "--timeout=2"},
		}),
		Check: all(exitNonzero, fasterThan(10000)),
	},
	{
		ID: "max_time", Finding: "H3", What: "--max-time 2 stops a hung request, as curl -m 2",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--max-time", "2", e.URL("/hang")) },
			"curl":  func(e *Env) Spec { return args("-sS", "--max-time", "2", e.URL("/hang")) },
		},
		Check: all(exitNonzero, fasterThan(10000)),
	},
	{
		ID: "connect_timeout", Finding: "H3", What: "--connect-timeout 2 stops a TLS handshake that never ends",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--connect-timeout", "2", e.SilentURL()) },
			"curl":  func(e *Env) Spec { return args("-sS", "--connect-timeout", "2", e.SilentURL()) },
		},
		Check: all(exitNonzero, fasterThan(10000)),
	},
	{
		ID: "speed_limit_stdout", Finding: "H3", What: "--speed-limit stops a body that trickles in",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--speed-limit", "1000", "--speed-time", "2", e.URL("/trickle")) },
			"curl":  func(e *Env) Spec { return args("-sS", "--speed-limit", "1000", "--speed-time", "2", e.URL("/trickle")) },
		},
		Check: all(exitNonzero, fasterThan(10000)),
	},
	{
		ID: "speed_limit_file", Finding: "H3", What: "--speed-limit stops a download to a file that trickles in",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec {
				return toFile("ghola", e.URL("/trickle"), "--speed-limit", "1000", "--speed-time", "2")
			},
			"curl": func(e *Env) Spec {
				return toFile("curl", e.URL("/trickle"), "--speed-limit", "1000", "--speed-time", "2")
			},
			"aria2": func(e *Env) Spec {
				return toFile("aria2", e.URL("/trickle"), "--max-tries=1", "--lowest-speed-limit=1000")
			},
		},
		Check: all(exitNonzero, fasterThan(20000)),
	},

	// ---- -n with requests that are not downloads ----
	{
		ID: "n_post", Finding: "H4", What: "-n does not send a POST more than once",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-n", "3", "-d", "once", e.URL("/echo")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-d", "once", e.URL("/echo")) },
		},
		Check: requestsAre("POST", 1),
	},
	{
		ID: "n_refused", Finding: "H4", What: "-n reports a refused connection",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-n", "3", e.DeadURL()) },
		},
		Check: all(exitNonzero, stderrNotEmpty),
	},

	// ---- Bad input ----
	{
		ID: "neg_retry", Finding: "H5", What: "-r -1 does not crash",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-r", "-1", e.URL("/file/1k")) },
		},
		Check: stderrLacks("panic:", "goroutine "),
	},
	{
		ID: "neg_timeout", Finding: "M7", What: "a negative timeout is rejected before any request",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--timeout", "-5", e.URL("/file/1k")) },
		},
		Check: all(exitNonzero, noRequests),
	},
	{
		ID: "zero_connections", Finding: "M7", What: "-n 0 is rejected before any request",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-n", "0", e.URL("/file/1k")) },
		},
		Check: all(exitNonzero, noRequests),
	},
	{
		ID: "header_no_colon", Finding: "M7", What: "a header without a colon gives a message",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-H", "NoColonHere", e.URL("/echo")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-H", "NoColonHere", e.URL("/echo")) },
		},
		Check: either(exitNonzero, stderrNotEmpty),
	},
	{
		ID: "header_remove", Finding: "M7", What: `-H "User-Agent:" removes the header`,
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-H", "User-Agent:", e.URL("/echo")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-H", "User-Agent:", e.URL("/echo")) },
		},
		Check: lastTo("/echo", "", headerAbsent("User-Agent")),
	},
	{
		ID: "header_crlf", Finding: "ok", What: "a CRLF in -H does not inject a second header",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-H", "X-A: 1\r\nX-Injected: yes", e.URL("/echo")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-H", "X-A: 1\r\nX-Injected: yes", e.URL("/echo")) },
		},
		Check: func(r *Result, e *Env) error {
			if len(r.Requests) == 0 {
				return nil // refusing the header is also safe
			}
			return lastTo("/echo", "", headerAbsent("X-Injected"))(r, e)
		},
	},

	// ---- Request defaults ----
	{
		ID: "get_no_content_type", Finding: "M1", What: "a GET has no Content-Type",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args(e.URL("/echo")) },
			"curl":  func(e *Env) Spec { return args("-sS", e.URL("/echo")) },
			"wget":  func(e *Env) Spec { return args("-q", "-O", "-", e.URL("/echo")) },
		},
		Check: lastTo("/echo", "", headerAbsent("Content-Type")),
	},
	{
		ID: "get_no_content_type_chrome", Finding: "M1", What: "an impersonated Chrome GET has no Content-Type",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--impersonate", "chrome", e.URL("/echo")) },
		},
		Check: lastTo("/echo", "", headerAbsent("Content-Type")),
	},
	{
		ID: "form_content_type", Finding: "M1", What: "-d form data is labelled as a form",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-d", "a=1&b=2", e.URL("/echo")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-d", "a=1&b=2", e.URL("/echo")) },
			"wget":  func(e *Env) Spec { return args("-q", "--post-data", "a=1&b=2", "-O", "-", e.URL("/echo")) },
		},
		Check: lastTo("/echo", "", headerIs("Content-Type", "application/x-www-form-urlencoded")),
	},
	{
		ID: "basic_auth_stdout", Finding: "ok", What: "Basic auth, body to stdout",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-u", "alice:s3cret", e.URL("/basic")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-u", "alice:s3cret", e.URL("/basic")) },
		},
		Check: all(exitZero, lastTo("/basic", "", headerIs("Authorization", "Basic YWxpY2U6czNjcmV0"))),
	},
	{
		ID: "basic_auth", Finding: "H8", What: "Basic auth on a download to a file",
		Runs: downloadTo("/basic", map[string][]string{
			"ghola": {"-u", "alice:s3cret"}, "curl": {"-u", "alice:s3cret"},
			"wget": {"--user", "alice", "--password", "s3cret"}, "aria2": {"--http-user=alice", "--http-passwd=s3cret"},
		}),
		Check: all(exitZero, fileIs("f", text("authorized\n"))),
	},
	{
		ID: "retry_503", Finding: "ok", What: "retry 503 until it succeeds",
		Runs: downloadTo("/flaky", map[string][]string{
			"ghola": {"-r", "3", "--retry-http"}, "curl": {"--retry", "3"},
			"wget": {"-t", "3", "--retry-on-http-error=503"}, "aria2": {"--max-tries=3", "--retry-wait=1"},
		}),
		Check: all(exitZero, fileIs("f", text("ok after retries\n"))),
	},

	// ---- Errors and exit codes ----
	{
		ID: "fail_to_file", Finding: "H7", What: "-f with -o writes no file on a 404",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-f", "-o", "f", e.URL("/status/404")) },
			"curl":  func(e *Env) Spec { return args("-sSf", "-o", "f", e.URL("/status/404")) },
		},
		Check: all(exitNonzero, fileAbsent("f")),
	},
	{
		ID: "exit_refused", Finding: "M6", What: "a refused connection exits 7, as curl does",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args(e.DeadURL()) },
			"curl":  func(e *Env) Spec { return args("-sS", e.DeadURL()) },
		},
		Check: exitIs(7),
	},
	{
		ID: "exit_dns", Finding: "M6", What: "an unknown host exits 6, as curl does",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(*Env) Spec { return args("http://no-such-host.invalid/") },
			"curl":  func(*Env) Spec { return args("-sS", "http://no-such-host.invalid/") },
		},
		Check: exitIs(6),
	},
	{
		ID: "exit_tls", Finding: "M6", What: "an untrusted certificate exits 60, as curl does",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args(e.TLSURL("/file/1k")) },
			"curl":  func(e *Env) Spec { return args("-sS", e.TLSURL("/file/1k")) },
		},
		Check: exitIs(60),
	},
	{
		ID: "exit_timeout", Finding: "M6", What: "a timeout exits 28, as curl does",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("--timeout", "2000", e.URL("/hang")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-m", "2", e.URL("/hang")) },
		},
		Check: exitIs(28),
	},

	// ---- TLS ----
	{
		ID: "tls_untrusted", Finding: "ok", What: "a self-signed certificate is refused by default",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args(e.TLSURL("/file/1k")) },
			"curl":  func(e *Env) Spec { return args("-sS", e.TLSURL("/file/1k")) },
			"wget":  func(e *Env) Spec { return args("-q", "-O", "-", e.TLSURL("/file/1k")) },
			"aria2": func(e *Env) Spec { return args("-q", "--allow-overwrite=true", "-o", "f", e.TLSURL("/file/1k")) },
		},
		Check: exitNonzero,
	},
	{
		ID: "tls_cacert", Finding: "ok", What: "a CA file makes the certificate trusted",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return toFile("ghola", e.TLSURL("/file/1k"), "--cacert", e.CAFile()) },
			"curl":  func(e *Env) Spec { return toFile("curl", e.TLSURL("/file/1k"), "--cacert", e.CAFile()) },
			"wget":  func(e *Env) Spec { return toFile("wget", e.TLSURL("/file/1k"), "--ca-certificate", e.CAFile()) },
			"aria2": func(e *Env) Spec { return toFile("aria2", e.TLSURL("/file/1k"), "--ca-certificate="+e.CAFile()) },
		},
		Check: all(exitZero, fileIs("f", ref("1k"))),
	},
	{
		ID: "tls_insecure", Finding: "ok", What: "-k skips the certificate check",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return toFile("ghola", e.TLSURL("/file/1k"), "-k") },
			"curl":  func(e *Env) Spec { return toFile("curl", e.TLSURL("/file/1k"), "-k") },
			"wget":  func(e *Env) Spec { return toFile("wget", e.TLSURL("/file/1k"), "--no-check-certificate") },
			"aria2": func(e *Env) Spec { return toFile("aria2", e.TLSURL("/file/1k"), "--check-certificate=false") },
		},
		Check: all(exitZero, fileIs("f", ref("1k"))),
	},

	// ---- File names ----
	{
		ID: "content_disposition_traversal", Finding: "ok", What: `Content-Disposition "../../evil.txt" stays in the working directory`,
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args("-O", "-J", e.URL("/cd-evil")) },
			"curl":  func(e *Env) Spec { return args("-sS", "-O", "-J", e.URL("/cd-evil")) },
			"wget":  func(e *Env) Spec { return args("-q", "--content-disposition", e.URL("/cd-evil")) },
			"aria2": func(e *Env) Spec { return args("-q", e.URL("/cd-evil")) },
		},
		Check: noEscape("evil.txt"),
	},

	// ---- Environment and URLs ----
	{
		ID: "proxy_env", Finding: "M8", What: "HTTP_PROXY is used",
		Runs: func() map[string]func(e *Env) Spec {
			runs := map[string]func(e *Env) Spec{
				"ghola": func(e *Env) Spec { return args(e.URL("/file/1k")) },
				"curl":  func(e *Env) Spec { return args("-sS", e.URL("/file/1k")) },
				"wget":  func(e *Env) Spec { return args("-q", "-t", "1", "-O", "-", e.URL("/file/1k")) },
				"aria2": func(e *Env) Spec {
					return args("-q", "--max-tries=1", "--allow-overwrite=true", "-o", "f", e.URL("/file/1k"))
				},
			}
			for name, mk := range runs {
				runs[name] = func(e *Env) Spec {
					return withEnv([]string{"HTTP_PROXY=" + e.DeadURL(), "http_proxy=" + e.DeadURL()}, mk)(e)
				}
			}
			return runs
		}(),
		Check: all(exitNonzero, noRequests), // the proxy is dead, so no request may reach the server
	},
	{
		ID: "url_no_scheme", Finding: "M9", What: "a URL without a scheme uses http://",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args(e.URL("/file/1k")[len("http://"):]) },
			"curl":  func(e *Env) Spec { return args("-sS", e.URL("/file/1k")[len("http://"):]) },
			"wget":  func(e *Env) Spec { return args("-q", "-O", "-", e.URL("/file/1k")[len("http://"):]) },
		},
		Check: all(exitZero, stdoutIs(ref("1k"))),
	},
	{
		ID: "two_urls", Finding: "M9", What: "two URLs give both bodies",
		Runs: map[string]func(e *Env) Spec{
			"ghola": func(e *Env) Spec { return args(e.URL("/file/1k"), e.URL("/file/2k")) },
			"curl":  func(e *Env) Spec { return args("-sS", e.URL("/file/1k"), e.URL("/file/2k")) },
			"wget":  func(e *Env) Spec { return args("-q", "-O", "-", e.URL("/file/1k"), e.URL("/file/2k")) },
		},
		Check: all(exitZero, stdoutIs(func(e *Env) []byte { return append(append([]byte(nil), e.Ref("1k")...), e.Ref("2k")...) })),
	},
}
