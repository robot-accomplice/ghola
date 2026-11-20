package cli

import (
	"fmt"
	"strings"
)

// The tool specs of the compatibility commands. Each table line maps the
// tool's names of one option to native arguments:
//
//	-L --location => --follow
//	-o --output   => -o %s
//
// "%s" stands for the value; a line with "%s" takes one. Each flag maps to
// the native option of the same meaning; docs/cli/v1-options.csv has the
// same equivalents in its curl, wget and aria2 columns.

// curl's defaults: no redirects (at most 50 with -L), no fail, no retries,
// no decompression.
const curlTable = `
-L --location                  => --follow
--location-trusted             => --follow-trusted
-f --fail --fail-with-body     => --fail
-k --insecure                  => --no-tls-verify
--compressed                   => --compressed
-I --head                      => --head
-i --include --show-headers    => --show-headers
-D --dump-header                => --save-headers %s
-z --time-cond                  => --cond-time %s
--etag-save                    => --cond-etag-save %s
--etag-compare                 => --cond-etag-compare %s
--path-as-is                   => --url-path-as-is
--request-target               => --request-target %s
-v --verbose                   => -v
-R --remote-time               => --output-remote-time
-O --remote-name               => -o ./
-J --remote-header-name        => --output-name header
-# --progress-bar              => --progress
--no-progress-meter            => --no-progress
-g --globoff                   => --no-url-glob
-o --output                    => -o %s
-H --header                    => -H %s
-A --user-agent                => --user-agent %s
-e --referer                   => --referer %s
-d --data --data-ascii --data-binary => -d %s
--data-raw                     => --data-literal %s
--data-urlencode               => --field %s
-F --form                      => --form %s
-X --request                   => -m %s
-u --user                      => -u %s
--basic                        => --auth basic
--digest                       => --auth digest
--anyauth                      => --auth any
--ntlm                         => --auth ntlm
--negotiate                    => --auth negotiate
-x --proxy                     => --proxy %s
-U --proxy-user                => --proxy-user %s
--noproxy                      => --proxy-bypass %s
--cacert                       => --tls-ca %s
-E --cert                      => --tls-cert %s
--key                          => --tls-key %s
-b --cookie                    => --cookie %s
-c --cookie-jar                => --cookie-jar %s
-r --range                     => --range %s
-m --max-time                  => --timeout %s
--connect-timeout              => --timeout-connect %s
-Y --speed-limit               => --stall-rate %s
-y --speed-time                => --stall-time %s
--limit-rate                   => --rate-max %s
--retry                        => -r %s
--retry-delay                  => --retry-delay %s
--retry-max-time               => --retry-max-time %s
--max-redirs                   => --follow-max %s
-T --upload-file               => --upload %s
--json                         => --json %s
--oauth2-bearer                => --auth-bearer %s
--url-query                    => --query %s
-n --netrc                     => --auth-netrc
--netrc-file                   => --auth-netrc-file %s
--pinnedpubkey                 => --tls-pin %s
--capath                       => --tls-ca-dir %s
--tlsv1.2                      => --tls-min 1.2
--tlsv1.3                      => --tls-min 1.3
--tls-max                      => --tls-max %s
--ciphers                      => --tls-ciphers %s
--crlfile                      => --tls-crl %s
--cert-status                  => --tls-ocsp
--cert-type                    => --tls-cert-type %s
--resolve                      => --resolve %s
--connect-to                   => --connect-to %s
-4 --ipv4                      => --ip 4
-6 --ipv6                      => --ip 6
--interface                    => --interface %s
--local-port                   => --local-port %s
--unix-socket                  => --unix-socket %s
--create-dirs                  => --output-create-dirs
--max-filesize                 => --max-size %s
`

// wget saves under the remote name (ghola download), follows up to 20
// redirects, and on an HTTP error writes no file, as ghola does by
// default. wget's own retry rules have no native form, so ghola wget
// retries only with -t. wget 1 asks for no compression.
const wgetTable = `
-q --quiet -nv --no-verbose    => -q
-v --verbose                   => -v
-c --continue                  => -c
--no-check-certificate         => --no-tls-verify
--content-disposition          => --output-name header
--connect-timeout              => --timeout-connect %s
--header                       => -H %s
-U --user-agent                => --user-agent %s
--referer                      => --referer %s
--post-data --body-data        => -d %s
--method                       => -m %s
--ca-certificate               => --tls-ca %s
--certificate                  => --tls-cert %s
--private-key                  => --tls-key %s
--ca-directory                 => --tls-ca-dir %s
--ciphers                      => --tls-ciphers %s
--crl-file                     => --tls-crl %s
--certificate-type             => --tls-cert-type %s
--limit-rate                   => --rate-max %s
--max-redirect                 => --follow-max %s
--load-cookies --save-cookies  => --cookie-jar %s
-4 --inet4-only                => --ip 4
-6 --inet6-only                => --ip 6
--bind-address                 => --interface %s
`

// aria2c saves under the remote name, follows redirects, and tries 5
// times with no wait (--max-tries 5, --retry-wait 0).
const aria2Table = `
-q --quiet                     => -q
-c --continue                  => -c
-R --remote-time               => --output-remote-time
-x --max-connection-per-server -s --split => -n %s
--header                       => -H %s
-U --user-agent                => --user-agent %s
--referer                      => --referer %s
--all-proxy                    => --proxy %s
--no-proxy                     => --proxy-bypass %s
--ca-certificate               => --tls-ca %s
--certificate                  => --tls-cert %s
--private-key                  => --tls-key %s
--min-tls-version              => --tls-min %s
--connect-timeout              => --timeout-connect %s
-t --timeout                   => --stall-time %s
--retry-wait                   => --retry-delay %s
--max-download-limit           => --rate-max %s
--disable-ipv6                 => --ip 4
--interface                    => --interface %s
-n --no-netrc                  => --no-auth-netrc
--netrc-path                   => --auth-netrc-file %s
--load-cookies --save-cookies  => --cookie-jar %s
`

// parseToolTable reads a table of "names => native arguments" lines.
func parseToolTable(text string) map[string]compatFlag {
	flags := map[string]compatFlag{}
	for _, line := range strings.Split(text, "\n") {
		names, args, ok := strings.Cut(line, "=>")
		if !ok {
			if strings.TrimSpace(line) != "" {
				panic(fmt.Sprintf("cli: bad tool table line %q", line))
			}
			continue
		}
		f := compatFlag{args: strings.Fields(args), value: strings.Contains(args, "%s")}
		for _, n := range strings.Fields(names) {
			flags[n] = f
		}
	}
	return flags
}

// words turns a space-separated list into a set.
func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

var curlSpec = &toolSpec{
	name:         JobCurl,
	defaults:     strings.Fields("--no-follow --follow-max 50 --no-fail -r 0 --no-compressed"),
	valueLetters: "ACDEFHKPQTUXYbcdehmortuwxyz", // curl 8.22.0 --help all
	flags:        parseToolTable(curlTable),
	takesValue:   words("-C --continue-at -K --config --url"),
	special:      curlSpecial,
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

var wgetSpec = &toolSpec{
	name:         JobWget,
	defaults:     strings.Fields("-r 0 --no-compressed --no-url-glob --auth-netrc"), // wget reads ~/.netrc by default
	valueLetters: "aABDeiIloOPQRtTUwX",                                              // the wget and Wget2 manuals
	equals:       true,
	// With -O, wget writes one URL to a file or stdout: a fetch, which also
	// takes a body. Without it, wget saves under the remote name.
	downloadWithoutFile: true,
	flags:               parseToolTable(wgetTable),
	ignored:             words("--no-config --keep-session-cookies --progress"),
	takesValue: words("-T --timeout -O --output-document -P --directory-prefix -t --tries " +
		"--user --password --http-user --http-password --post-file --body-file --progress"),
	special: wgetSpecial,
}

func wgetSpecial(t *translation, name, value string, _ bool) (bool, error) {
	switch name {
	case "-O", "--output-document":
		t.file = value // - is stdout, as in ghola
	case "-P", "--directory-prefix":
		t.dir = value
		t.out = append(t.out, "--output-create-dirs") // wget creates the prefix folder
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

var aria2Spec = &toolSpec{
	name:         JobAria2c,
	defaults:     strings.Fields("download -r 4 --retry-delay 0 --no-compressed --no-url-glob --auth-netrc"), // aria2c reads ~/.netrc by default
	valueLetters: "MOTUdijklmostux",                                                                          // aria2c 1.37.0 --help=#all
	equals:       true,
	flags:        parseToolTable(aria2Table),
	ignored: words("--no-conf -k --min-split-size --console-log-level " +
		"--summary-interval --download-result"),
	takesValue: words("-o --out -d --dir -m --max-tries --http-user --http-passwd " +
		"--allow-overwrite --check-certificate -k --min-split-size --console-log-level " +
		"--summary-interval --download-result"),
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
		if value == "true" {
			t.out = append(t.out, "--output-overwrite")
		}
	default:
		return false, nil
	}
	return true, nil
}
