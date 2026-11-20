package cli

// The tool specs of the compatibility commands. Each flag maps to the
// native option of the same meaning; docs/cli/v1-options.csv has the same
// equivalents in its curl, wget and aria2 columns.

// curlSpec: curl's defaults are no redirects (at most 50 with -L), no
// fail, no retries and no decompression.
var curlSpec = &toolSpec{
	name:         "curl",
	defaults:     []string{"--no-follow", "--follow-max", "50", "--no-fail", "-r", "0", "--no-compressed"},
	valueLetters: "ACDEFHKPQTUXYbcdehmortuwxyz", // curl 8.22.0 --help all
	flags: map[string]compatFlag{
		"-L": flag("--follow"), "--location": flag("--follow"),
		"--location-trusted": flag("--follow-trusted"),
		"-f":                 flag("--fail"), "--fail": flag("--fail"), "--fail-with-body": flag("--fail"),
		"-k": flag("--no-tls-verify"), "--insecure": flag("--no-tls-verify"),
		"--compressed": flag("--compressed"),
		"-I":           flag("--head"), "--head": flag("--head"),
		"-i": flag("--show-headers"), "--include": flag("--show-headers"), "--show-headers": flag("--show-headers"),
		"-v": flag("-v"), "--verbose": flag("-v"),
		"-R": flag("--output-remote-time"), "--remote-time": flag("--output-remote-time"),
		"-O": flag("-o", "./"), "--remote-name": flag("-o", "./"),
		"-J": flag("--output-name", "header"), "--remote-header-name": flag("--output-name", "header"),
		"-#": flag("--progress"), "--progress-bar": flag("--progress"),
		"--no-progress-meter": flag("--no-progress"),
		"-g":                  flag("--no-url-glob"), "--globoff": flag("--no-url-glob"),
		"-o": valued("-o", "%s"), "--output": valued("-o", "%s"),
		"-H": valued("-H", "%s"), "--header": valued("-H", "%s"),
		"-A": valued("--user-agent", "%s"), "--user-agent": valued("--user-agent", "%s"),
		"-e": valued("--referer", "%s"), "--referer": valued("--referer", "%s"),
		"-d": valued("-d", "%s"), "--data": valued("-d", "%s"), "--data-ascii": valued("-d", "%s"),
		"--data-binary": valued("-d", "%s"), "--data-raw": valued("--data-literal", "%s"),
		"--data-urlencode": valued("--field", "%s"),
		"-F":               valued("--form", "%s"), "--form": valued("--form", "%s"),
		"-X": valued("-m", "%s"), "--request": valued("-m", "%s"),
		"-u": valued("-u", "%s"), "--user": valued("-u", "%s"),
		"-x": valued("--proxy", "%s"), "--proxy": valued("--proxy", "%s"),
		"-U": valued("--proxy-user", "%s"), "--proxy-user": valued("--proxy-user", "%s"),
		"--cacert": valued("--tls-ca", "%s"),
		"-E":       valued("--tls-cert", "%s"), "--cert": valued("--tls-cert", "%s"), "--key": valued("--tls-key", "%s"),
		"-b": valued("--cookie", "%s"), "--cookie": valued("--cookie", "%s"),
		"-c": valued("--cookie-jar", "%s"), "--cookie-jar": valued("--cookie-jar", "%s"),
		"-r": valued("--range", "%s"), "--range": valued("--range", "%s"),
		"-m": valued("--timeout", "%s"), "--max-time": valued("--timeout", "%s"),
		"--connect-timeout": valued("--timeout-connect", "%s"),
		"-Y":                valued("--stall-rate", "%s"), "--speed-limit": valued("--stall-rate", "%s"),
		"-y": valued("--stall-time", "%s"), "--speed-time": valued("--stall-time", "%s"),
		"--limit-rate": valued("--rate-max", "%s"),
		"--retry":      valued("-r", "%s"), "--retry-delay": valued("--retry-delay", "%s"),
		"--max-redirs": valued("--follow-max", "%s"),
		"-T":           valued("--upload", "%s"), "--upload-file": valued("--upload", "%s"),
	},
	takesValue: map[string]bool{"-C": true, "--continue-at": true, "-K": true, "--config": true, "--url": true},
	special:    curlSpecial,
}

func curlSpecial(t *translation, name, value string, _ bool) (bool, error) {
	switch name {
	case "-s", "--silent":
		t.silent = true
		t.out = append(t.out, "-q")
	case "-S", "--show-error":
		t.showError = true
	case "-C", "--continue-at":
		if value == "-" {
			t.out = append(t.out, "-c")
		} else {
			t.out = append(t.out, "--continue-at", value)
		}
	case "-K", "--config":
		return true, t.config(value)
	case "--url":
		t.tail = append(t.tail, value)
	default:
		return false, nil
	}
	return true, nil
}

// wgetSpec: wget saves under the remote name (ghola download), follows up
// to 20 redirects, and on an HTTP error writes no file, as ghola does by
// default. wget's own retry rules have no native form, so ghola wget
// retries only with -t. wget 1 asks for no compression.
var wgetSpec = &toolSpec{
	name:         "wget",
	defaults:     []string{"-r", "0", "--no-compressed"},
	valueLetters: "aABDeiIloOPQRtTUwX", // the wget and Wget2 manuals
	// With -O, wget writes one URL to a file or stdout: a fetch, which also
	// takes a body. Without it, wget saves under the remote name.
	downloadWithoutFile: true,
	equals:              true,
	flags: map[string]compatFlag{
		"-q": flag("-q"), "--quiet": flag("-q"), "-nv": flag("-q"), "--no-verbose": flag("-q"),
		"-v": flag("-v"), "--verbose": flag("-v"),
		"-c": flag("-c"), "--continue": flag("-c"),
		"--no-check-certificate": flag("--no-tls-verify"),
		"--content-disposition":  flag("--output-name", "header"),
		"--connect-timeout":      valued("--timeout-connect", "%s"),
		"--header":               valued("-H", "%s"),
		"-U":                     valued("--user-agent", "%s"), "--user-agent": valued("--user-agent", "%s"),
		"--referer":        valued("--referer", "%s"),
		"--post-data":      valued("-d", "%s"),
		"--body-data":      valued("-d", "%s"),
		"--method":         valued("-m", "%s"),
		"--ca-certificate": valued("--tls-ca", "%s"),
		"--certificate":    valued("--tls-cert", "%s"),
		"--private-key":    valued("--tls-key", "%s"),
		"--limit-rate":     valued("--rate-max", "%s"),
		"--max-redirect":   valued("--follow-max", "%s"),
		"--load-cookies":   valued("--cookie-jar", "%s"),
		"--save-cookies":   valued("--cookie-jar", "%s"),
	},
	ignored:    map[string]bool{"--no-config": true, "--keep-session-cookies": true, "--progress": true},
	takesValue: map[string]bool{"-T": true, "--timeout": true, "-O": true, "--output-document": true, "-P": true, "--directory-prefix": true, "-t": true, "--tries": true, "--user": true, "--password": true, "--http-user": true, "--http-password": true, "--post-file": true, "--body-file": true, "--progress": true},
	special:    wgetSpecial,
}

func wgetSpecial(t *translation, name, value string, _ bool) (bool, error) {
	switch name {
	case "-O", "--output-document":
		t.file = value // - is stdout, as in ghola
	case "-P", "--directory-prefix":
		t.dir = value
	case "-t", "--tries":
		return true, t.tries(value)
	case "-T", "--timeout":
		// wget -T sets the DNS, connect and read timeouts.
		t.out = append(t.out, "--timeout-connect", value, "--stall-time", value)
	case "--user", "--http-user":
		t.user = value
	case "--password", "--http-password":
		t.pass = value
	case "--post-file", "--body-file":
		t.out = append(t.out, "-d", "@"+value)
	default:
		return false, nil
	}
	return true, nil
}

// aria2Spec: aria2c saves under the remote name, follows redirects, and
// tries 5 times with no wait (--max-tries 5, --retry-wait 0).
var aria2Spec = &toolSpec{
	name:         "aria2c",
	defaults:     []string{"download", "-r", "4", "--retry-delay", "0", "--no-compressed"},
	valueLetters: "MOTUdijklmostux", // aria2c 1.37.0 --help=#all
	equals:       true,
	flags: map[string]compatFlag{
		"-q": flag("-q"), "--quiet": flag("-q"),
		"-c": flag("-c"), "--continue": flag("-c"),
		"-R": flag("--output-remote-time"), "--remote-time": flag("--output-remote-time"),
		"-x": valued("-n", "%s"), "--max-connection-per-server": valued("-n", "%s"),
		"-s": valued("-n", "%s"), "--split": valued("-n", "%s"),
		"--header": valued("-H", "%s"),
		"-U":       valued("--user-agent", "%s"), "--user-agent": valued("--user-agent", "%s"),
		"--referer":         valued("--referer", "%s"),
		"--all-proxy":       valued("--proxy", "%s"),
		"--ca-certificate":  valued("--tls-ca", "%s"),
		"--certificate":     valued("--tls-cert", "%s"),
		"--private-key":     valued("--tls-key", "%s"),
		"--connect-timeout": valued("--timeout-connect", "%s"),
		"-t":                valued("--stall-time", "%s"), "--timeout": valued("--stall-time", "%s"), // no data for N seconds
		"--retry-wait":         valued("--retry-delay", "%s"),
		"--max-download-limit": valued("--rate-max", "%s"),
		"--load-cookies":       valued("--cookie-jar", "%s"),
		"--save-cookies":       valued("--cookie-jar", "%s"),
	},
	ignored: map[string]bool{
		"--no-conf": true, "-k": true, "--min-split-size": true, "--console-log-level": true,
		"--summary-interval": true, "--download-result": true,
	},
	takesValue: map[string]bool{"-o": true, "--out": true, "-d": true, "--dir": true, "-m": true, "--max-tries": true,
		"--http-user": true, "--http-passwd": true, "--allow-overwrite": true, "--check-certificate": true,
		"-k": true, "--min-split-size": true, "--console-log-level": true, "--summary-interval": true, "--download-result": true},
	special: aria2Special,
}

func aria2Special(t *translation, name, value string, _ bool) (bool, error) {
	switch name {
	case "-o", "--out":
		t.file = value
	case "-d", "--dir":
		t.dir = value
	case "-m", "--max-tries":
		return true, t.tries(value)
	case "--http-user":
		t.user = value
	case "--http-passwd":
		t.pass = value
	case "--check-certificate":
		if value == "false" {
			t.out = append(t.out, "--no-tls-verify")
		}
	case "--allow-overwrite":
		// ghola writes over an existing -o file today; the switch for the
		// download job (--output-overwrite) is phase 2.
		if value != "true" {
			return true, usagef("ghola aria2c: --allow-overwrite=%s maps to --output-overwrite, which ghola aria2c does not run yet", value)
		}
	default:
		return false, nil
	}
	return true, nil
}
