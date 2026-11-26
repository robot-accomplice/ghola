package cli

import (
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
	// wholeShorts are the options of two letters after one dash (wget 1
	// -nv, -nH, -np): they are not two short options.
	wholeShorts map[string]bool
	// finish adds the native options that depend on several of the tool's
	// options (wget -r without -np, -H with -D). nil for none.
	finish func(t *translation)
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
	// job is the native job that the options start (wget -r: mirror).
	job string
	// wget -r state: -np, -N, -H, -D, -l.
	noParent, timestamping, spanHosts, domains, level bool
	body                                              curlBody  // curl's data options
	vars                                              variables // curl's --variable values
	configDepth                                       int       // the -K files being read
	output                                            curlOutput
}

func translate(spec *toolSpec, args []string) (*Compat, error) {
	t := &translation{spec: spec, out: append([]string(nil), spec.defaults...), vars: variables{}}
	if err := t.run(args); err != nil {
		return nil, err
	}
	if spec.finish != nil {
		spec.finish(t)
	}
	if t.user != "" || t.pass != "" {
		t.out = append(t.out, "-u", t.user+":"+t.pass)
	}
	if out := joinOutput(t.dir, t.file); out != "" {
		t.out = append(t.out, "-o", out)
	}
	out := append(t.out, t.tail...)
	switch {
	case t.job != "":
		out = append([]string{t.job}, out...)
	case spec.downloadWithoutFile && t.file == "":
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
		case t.spec.wholeShorts[a]:
			err = t.option(a, "", false)
		default:
			i, err = t.shorts(a, args, i)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *translation) needsValue(name string) bool { return t.spec.valueTaker(name) }

// valueTaker reports whether the option takes a value.
func (s *toolSpec) valueTaker(name string) bool {
	if f, ok := s.flags[name]; ok {
		return f.value
	}
	return s.takesValue[name]
}

// long handles --name, --name=value (wget, aria2c) and --name value.
func (t *translation) long(a string, args []string, i int) (int, error) {
	if a == "--show-native" {
		t.showNative = true
		return i, nil
	}
	name, value, hasValue := strings.Cut(a, "=")
	if hasValue && !t.spec.equals && !t.needsValue("--"+strings.TrimPrefix(name[2:], expandPrefix)) && !t.needsValue(name) {
		name, value, hasValue = a, "", false // curl takes --name=value only for an option with a value
	}
	expand := false
	if rest, ok := strings.CutPrefix(name, "--"+expandPrefix); ok && t.needsValue("--"+rest) {
		name, expand = "--"+rest, true // curl's --expand-NAME
	}
	if !hasValue && t.needsValue(name) {
		if i+1 >= len(args) {
			return i, usagef("ghola %s: %s needs a value", t.spec.name, name)
		}
		i++
		value, hasValue = args[i], true
	}
	if expand {
		return i, t.expandOption(name, value)
	}
	return i, t.option(name, value, hasValue)
}

// expandOption translates curl's --expand-NAME VALUE to the --expand- form
// of the native option, so the native parser expands the variables.
func (t *translation) expandOption(name, value string) error {
	if kind, ok := curlDataKinds[name]; ok && t.spec.name == JobCurl {
		expanded, err := t.vars.expand(value) // the body is built here, not by the native parser
		if err != nil {
			return usagef("ghola curl: --%s%s: %v", expandPrefix, strings.TrimPrefix(name, "--"), err)
		}
		return t.addData(kind, expanded)
	}
	f, ok := t.spec.flags[name]
	if !ok {
		return t.notMapped(name)
	}
	out := make([]string, len(f.args))
	expanded := false
	for k, a := range f.args {
		out[k] = strings.ReplaceAll(a, "%s", value)
		if long := nativeLong(f.args[max(k-1, 0)]); a == "%s" && k > 0 && long != "" {
			out[k-1], expanded = "--"+expandPrefix+long, true
		}
	}
	if !expanded {
		return usagef("ghola %s: --%s%s is not supported", t.spec.name, expandPrefix, strings.TrimPrefix(name, "--"))
	}
	t.out = append(t.out, out...)
	return nil
}

// nativeLong is the long name of a native flag such as -H or --json.
func nativeLong(flag string) string {
	if long, ok := strings.CutPrefix(flag, "--"); ok {
		return long
	}
	if len(flag) == 2 && flag[0] == '-' {
		if o := lookupShort(flag[1]); o != nil {
			return o.Name
		}
	}
	return ""
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

// notMapped names an option that the command cannot run. When the option
// table names a native option for it, the message says so.
func (t *translation) notMapped(name string) error {
	for _, o := range otherIndex[name] {
		if o.tool == t.spec.name {
			return usagef("ghola %s: %s maps to --%s, which ghola %s does not run yet", t.spec.name, name, o.opt.Name, t.spec.name)
		}
	}
	return usagef("ghola %s: %s is not supported", t.spec.name, name)
}

// curlConfigDepth is how deep -K files can read other -K files, as in
// curl. A file that reads itself stops there with an argument error.
const curlConfigDepth = 5

// config reads a curl -K file ("-" is stdin) and translates its lines.
func (t *translation) config(path string) error {
	if t.configDepth >= curlConfigDepth {
		return usagef("ghola curl -K %s: -K files read other -K files more than %d levels deep", path, curlConfigDepth)
	}
	text, err := readDataFile(path)
	if err != nil {
		return usagef("ghola curl -K: %v", err)
	}
	lines, err := curlrc.Lines(text)
	if err != nil {
		return usagef("ghola curl -K %s: %v", path, err)
	}
	t.configDepth++
	defer func() { t.configDepth-- }()
	for _, l := range lines {
		args := []string{l.Opt}
		if l.HasValue {
			if !t.optionTakesValue(l.Opt) {
				return usagef("ghola curl -K %s, line %d: %s takes no value, but the line gives %q", path, l.N, l.Opt, l.Value)
			}
			args = append(args, l.Value)
		}
		if err := t.run(args); err != nil {
			return err
		}
	}
	return nil
}

// optionTakesValue reports whether an option, as a -K line names it,
// takes the next word as its value: a long option that needs one and has
// no "=", or a letter group that ends in a letter that takes one.
func (t *translation) optionTakesValue(opt string) bool {
	if long, ok := strings.CutPrefix(opt, "--"); ok {
		if strings.Contains(long, "=") {
			return false
		}
		return t.needsValue("--"+strings.TrimPrefix(long, expandPrefix)) || t.needsValue(opt)
	}
	letters := strings.TrimPrefix(opt, "-")
	for j := 0; j < len(letters); j++ {
		if strings.ContainsRune(t.spec.valueLetters, rune(letters[j])) {
			return j == len(letters)-1
		}
	}
	return false
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
