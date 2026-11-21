package cli

import (
	"os"
	"strconv"
	"strings"

	"github.com/robot-accomplice/ghola/internal/curlrc"
)

// A compatibility command (ghola curl, ghola wget, ghola aria2c) takes the
// syntax of the tool it copies and runs it as native arguments, on the
// same parser and option table, so the two cannot drift (v1 spec,
// section 6). The tool's defaults go first as native options, so the
// tool's options after them win.

// toolSpec describes one tool's syntax.
type toolSpec struct {
	name     string
	defaults []string
	// valueLetters are the short options that take a value.
	valueLetters string
	flags        map[string]compatFlag
	// ignored options tune speed or logging and change no result; they are
	// accepted and dropped. Every other unknown option is an error.
	ignored map[string]bool
	// special handles the options that need state (curl -s and -S, wget
	// --user and --password, -K files). It returns false for others.
	special func(t *translation, name, value string, hasValue bool) (handled bool, err error)
	// takesValue reports options that take a value although the flag map
	// does not translate them (special and ignored ones).
	takesValue map[string]bool
	equals     bool // the tool writes --name=value
	// downloadWithoutFile starts ghola download when no output file is
	// given (wget saves under the remote name).
	downloadWithoutFile bool
}

// compatFlag is the native form of one option: args, with "%s" for the
// value of an option that takes one.
type compatFlag struct {
	args  []string
	value bool
}

// Compat is the native form of a compatibility command line.
type Compat struct {
	Args []string // native arguments, the tool's defaults first
	// HideErrors is curl -s without -S: curl drops its error message.
	HideErrors bool
	// ShowNative is --show-native: print Args and run nothing.
	ShowNative bool
}

// Curl is kept for the callers of FromCurl.
type Curl = Compat

// FromCurl translates curl arguments.
func FromCurl(args []string) (*Compat, error) {
	if len(args) > 0 && (args[0] == "-q" || args[0] == "--disable") {
		args = args[1:] // curl: do not read .curlrc; ghola curl never reads it
	}
	// curl -: / --next starts the options of the next URL. Each group
	// translates on its own, with curl's defaults, and the native groups
	// join with --next.
	var out *Compat
	start := 0
	for i := 0; i <= len(args); i++ {
		if i < len(args) && args[i] != "-:" && args[i] != "--next" {
			continue
		}
		c, err := translate(curlSpec, args[start:i])
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = c
		} else {
			out.Args = append(append(out.Args, "--next"), c.Args...)
			out.ShowNative = out.ShowNative || c.ShowNative
		}
		start = i + 1
	}
	return out, nil
}

// FromWget translates wget (and Wget2) arguments.
func FromWget(args []string) (*Compat, error) { return translate(wgetSpec, args) }

// FromAria2c translates aria2c arguments.
func FromAria2c(args []string) (*Compat, error) { return translate(aria2Spec, args) }

type translation struct {
	spec       *toolSpec
	out        []string
	tail       []string // native arguments that go after the others (URLs)
	silent     bool
	showError  bool
	showNative bool
	user, pass string // wget --user and --password, joined at the end
	// dir and file are the folder and the file name of wget -P/-O and
	// aria2c -d/-o; they join into one -o at the end.
	dir, file string
	done      bool
}

func translate(spec *toolSpec, args []string) (*Compat, error) {
	t := &translation{spec: spec, out: append([]string(nil), spec.defaults...)}
	if err := t.run(args); err != nil {
		return nil, err
	}
	if t.user != "" || t.pass != "" {
		t.out = append(t.out, "-u", t.user+":"+t.pass)
	}
	if out := joinOutput(t.dir, t.file); out != "" {
		t.out = append(t.out, "-o", out)
	}
	out := append(t.out, t.tail...)
	if spec.downloadWithoutFile && t.file == "" {
		out = append([]string{JobDownload}, out...)
	}
	return &Compat{Args: out, HideErrors: t.silent && !t.showError, ShowNative: t.showNative}, nil
}

func (t *translation) run(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		var err error
		switch {
		case t.done || a == "-" || !strings.HasPrefix(a, "-"):
			t.tail = append(t.tail, a)
		case a == "--":
			t.done = true
		case strings.HasPrefix(a, "--"):
			i, err = t.long(a, args, i)
		default:
			i, err = t.shorts(a, args, i)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *translation) needsValue(name string) bool {
	if f, ok := t.spec.flags[name]; ok {
		return f.value
	}
	return t.spec.takesValue[name]
}

// long handles --name, --name=value (wget, aria2c) and --name value.
func (t *translation) long(a string, args []string, i int) (int, error) {
	if a == "--show-native" {
		t.showNative = true
		return i, nil
	}
	name, value, hasValue := strings.Cut(a, "=")
	if hasValue && !t.spec.equals {
		name, value, hasValue = a, "", false // curl has no --name=value
	}
	if !hasValue && t.needsValue(name) {
		if i+1 >= len(args) {
			return i, usagef("ghola %s: %s needs a value", t.spec.name, name)
		}
		i++
		value, hasValue = args[i], true
	}
	return i, t.option(name, value, hasValue)
}

// shorts handles a letter group such as -sSL, -qO- or -x16.
func (t *translation) shorts(a string, args []string, i int) (int, error) {
	letters := a[1:]
	for j := 0; j < len(letters); j++ {
		name := "-" + string(letters[j])
		if !strings.ContainsRune(t.spec.valueLetters, rune(letters[j])) {
			if err := t.option(name, "", false); err != nil {
				return i, err
			}
			continue
		}
		value := letters[j+1:]
		if value == "" {
			if i+1 >= len(args) {
				return i, usagef("ghola %s: %s needs a value", t.spec.name, name)
			}
			i++
			value = args[i]
		}
		return i, t.option(name, value, true)
	}
	return i, nil
}

// option translates one option.
func (t *translation) option(name, value string, hasValue bool) error {
	if t.spec.special != nil {
		if handled, err := t.spec.special(t, name, value, hasValue); handled || err != nil {
			return err
		}
	}
	if t.spec.ignored[name] {
		return nil
	}
	f, ok := t.spec.flags[name]
	if !ok {
		return t.notMapped(name)
	}
	for _, a := range f.args {
		t.out = append(t.out, strings.ReplaceAll(a, "%s", value))
	}
	return nil
}

// joinOutput is the -o of a folder and a file name: "dir/" for the remote
// name inside the folder, "dir/name", or "name".
func joinOutput(dir, file string) string {
	switch {
	case file == "-" || dir == "":
		return file
	case file == "":
		return strings.TrimSuffix(dir, "/") + "/"
	default:
		return strings.TrimSuffix(dir, "/") + "/" + file
	}
}

// jobOptions are options that start another job in the tool they come
// from, with the native job that does it.
var jobOptions = map[string]string{
	"-r": mirrorJob, "--recursive": mirrorJob, "-m": mirrorJob, "--mirror": mirrorJob,
	"-i": inputJob, "--input-file": inputJob,
}

const (
	mirrorJob = "ghola mirror"
	inputJob  = "ghola download --input"
)

// notMapped names an option that the command cannot run. When the option
// table names a native option for it, the message says so.
func (t *translation) notMapped(name string) error {
	if job, ok := jobOptions[name]; ok && t.spec.name == "wget" {
		return usagef("ghola wget: %s starts another job: %s, which is not built yet", name, job)
	}
	for _, o := range otherIndex[name] {
		if o.tool == t.spec.name {
			return usagef("ghola %s: %s maps to --%s, which ghola %s does not run yet", t.spec.name, name, o.opt.Name, t.spec.name)
		}
	}
	return usagef("ghola %s: %s is not supported", t.spec.name, name)
}

// config reads a curl -K file and translates its arguments.
func (t *translation) config(path string) error {
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

// tries turns a try count (wget -t, aria2c -m) into retries. 0 tries means
// no limit in both tools; ghola stops at 100.
func (t *translation) tries(v string) error {
	n, err := strconv.Atoi(v)
	switch {
	case err != nil || n < 0:
		return usagef("ghola %s: %q is not a try count", t.spec.name, v)
	case n == 0:
		n = 101
	}
	t.out = append(t.out, "-r", strconv.Itoa(n-1))
	return nil
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
