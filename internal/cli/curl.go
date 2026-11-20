package cli

import (
	"os"
	"strings"

	"github.com/robot-accomplice/ghola/internal/curlrc"
)

// curlDefaults are curl's defaults where they differ from ghola's (v1
// spec, section 6: a compatibility command uses the defaults of the tool
// it copies). They go first, so a curl option after them wins.
var curlDefaults = []string{"--no-follow", "--follow-max", "50", "--no-fail", "-r", "0", "--no-compressed"}

// curlValueLetters are curl's short options that take a value, from
// curl 8.22.0 --help all (docs/cli/flag-inventory.csv).
const curlValueLetters = "ACDEFHKPQTUXYbcdehmortuwxyz"

// curlFlag is the native form of one curl option. args are the native
// arguments; for an option with a value, "%s" stands for the value.
type curlFlag struct {
	args  []string
	value bool
}

func flag(args ...string) curlFlag   { return curlFlag{args: args} }
func valued(args ...string) curlFlag { return curlFlag{args: args, value: true} }

// curlFlags maps curl's options to native ones. An option that is not
// here and not in the curl column of the option table is refused.
var curlFlags = map[string]curlFlag{
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
}

// Curl is the native form of a curl command line.
type Curl struct {
	Args []string // native arguments, curl's defaults first
	// HideErrors is curl -s without -S: curl drops its error message.
	HideErrors bool
	// ShowNative is --show-native: print Args and run nothing.
	ShowNative bool
}

// FromCurl translates curl arguments into native arguments, so ghola curl
// runs on the same parser and the same option table as the native
// command (v1 spec, section 6).
func FromCurl(args []string) (*Curl, error) {
	t := &curlTranslation{out: append([]string(nil), curlDefaults...)}
	if len(args) > 0 && (args[0] == "-q" || args[0] == "--disable") {
		args = args[1:] // curl: do not read .curlrc; ghola curl never reads it
	}
	if err := t.run(args); err != nil {
		return nil, err
	}
	return &Curl{Args: t.out, HideErrors: t.silent && !t.showError, ShowNative: t.showNative}, nil
}

type curlTranslation struct {
	out                           []string
	silent, showError, showNative bool
	done                          bool // after "--"
}

func (t *curlTranslation) run(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case t.done || a == "-" || !strings.HasPrefix(a, "-"):
			t.out = append(t.out, a)
		case a == "--":
			t.done = true
		case strings.HasPrefix(a, "--"):
			next, err := t.long(a, args, i)
			if err != nil {
				return err
			}
			i = next
		default:
			next, err := t.shorts(a, args, i)
			if err != nil {
				return err
			}
			i = next
		}
	}
	return nil
}

// long handles one --option; it returns the index of the last argument it
// used.
func (t *curlTranslation) long(a string, args []string, i int) (int, error) {
	switch a {
	case "--silent":
		t.silent = true
		t.out = append(t.out, "-q")
		return i, nil
	case "--show-error":
		t.showError = true
		return i, nil
	case "--show-native":
		t.showNative = true
		return i, nil
	case "--url", "--config":
		if i+1 >= len(args) {
			return i, usagef("ghola curl: %s needs a value", a)
		}
		if a == "--url" {
			t.out = append(t.out, args[i+1])
			return i + 1, nil
		}
		return i + 1, t.config(args[i+1])
	}
	return t.option(a, args, i)
}

// shorts handles a letter group such as -sSL or -ofile.
func (t *curlTranslation) shorts(a string, args []string, i int) (int, error) {
	letters := a[1:]
	for j := 0; j < len(letters); j++ {
		c := letters[j]
		switch c {
		case 's':
			t.silent = true
			t.out = append(t.out, "-q")
			continue
		case 'S':
			t.showError = true
			continue
		}
		flagName := "-" + string(c)
		if !strings.ContainsRune(curlValueLetters, rune(c)) {
			if _, err := t.option(flagName, nil, 0); err != nil {
				return i, err
			}
			continue
		}
		if rest := letters[j+1:]; rest != "" {
			_, err := t.option(flagName, []string{flagName, rest}, 0)
			return i, err
		}
		return t.option(flagName, args, i)
	}
	return i, nil
}

// option translates one curl option. For an option with a value, the
// value is args[i+1]; the result is the index of the last used argument.
func (t *curlTranslation) option(name string, args []string, i int) (int, error) {
	f, ok := curlFlags[name]
	if !ok {
		return i, t.notMapped(name)
	}
	if !f.value {
		t.out = append(t.out, f.args...)
		return i, nil
	}
	if i+1 >= len(args) {
		return i, usagef("ghola curl: %s needs a value", name)
	}
	value := args[i+1]
	switch name {
	case "-C", "--continue-at":
		return i + 1, t.continueAt(value)
	case "-K":
		return i + 1, t.config(value)
	}
	for _, a := range f.args {
		t.out = append(t.out, strings.ReplaceAll(a, "%s", value))
	}
	return i + 1, nil
}

func (t *curlTranslation) continueAt(v string) error {
	if v == "-" {
		t.out = append(t.out, "-c")
	} else {
		t.out = append(t.out, "--continue-at", v)
	}
	return nil
}

// config reads a -K file in curl's syntax and translates its arguments.
func (t *curlTranslation) config(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return usagef("ghola curl -K: %v", err)
	}
	args, err := curlrc.Args(string(b))
	if err != nil {
		return usagef("ghola curl -K %s: %v", path, err)
	}
	return t.run(args)
}

// notMapped names a curl option that ghola curl cannot run. When the
// option table names a native option for it, the message says so; that
// option belongs to a phase that is not built, or has no curl form.
func (t *curlTranslation) notMapped(name string) error {
	for _, o := range otherIndex[name] {
		if o.tool == "curl" {
			return usagef("ghola curl: %s maps to --%s, which ghola curl does not run yet", name, o.opt.Name)
		}
	}
	return usagef("ghola curl: %s is not supported", name)
}

func init() {
	for _, n := range []string{"-C", "--continue-at", "-K"} {
		curlFlags[n] = valued("%s")
	}
}

// ShellQuote joins args as a shell command line, for --show-native.
func ShellQuote(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
			q[i] = a
			continue
		}
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
