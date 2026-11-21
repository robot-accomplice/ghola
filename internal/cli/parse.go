package cli

import (
	"fmt"
	"strings"
)

// The jobs (v1 spec, section 1). Fetch is the job without a word.
const (
	JobFetch      = ""
	JobDownload   = "download"
	JobMirror     = "mirror"
	JobJobs       = "jobs"
	JobServe      = "serve"
	JobInspect    = "inspect"
	JobExplain    = "explain"
	JobProfiles   = "profiles"
	JobCompletion = "completion"
	JobCurl       = "curl"
	JobWget       = "wget"
	JobAria2c     = "aria2c"
)

var jobs = map[string]bool{
	JobDownload: true, JobMirror: true, JobJobs: true, JobServe: true, JobInspect: true,
	JobExplain: true, JobProfiles: true, JobCompletion: true, JobCurl: true, JobWget: true, JobAria2c: true,
}

// compatJobs take the syntax of the tool they copy; Parse does not read
// their arguments.
var compatJobs = map[string]bool{JobCurl: true, JobWget: true, JobAria2c: true}

// transferJobs take the transfer options (scope "all"). inspect makes a
// request as a fetch does, so it takes the fetch options too (with
// --impersonate or --proxy, for example).
var transferJobs = map[string]bool{JobFetch: true, JobDownload: true, JobMirror: true, JobInspect: true}

// Command is a parsed command line.
type Command struct {
	Job string
	// Values holds each option that the command line set, by name, in the
	// order given. A boolean holds "true" or "false". A repeated option
	// has several values.
	Values map[string][]string
	// Args are the other arguments: the URLs of a transfer, the operands of
	// another job, or the raw arguments of a compatibility command.
	Args []string
}

// Has reports whether the command line set the option.
func (c *Command) Has(name string) bool { return len(c.Values[name]) > 0 }

// Last returns the last value of the option, or "".
func (c *Command) Last(name string) string {
	v := c.Values[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// UsageError is an argument error (exit 2). The message says what to type.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, a ...any) error { return &UsageError{Msg: fmt.Sprintf(format, a...)} }

// Parse reads a v1 command line (without the program name).
func Parse(args []string) (*Command, error) {
	c := &Command{Values: map[string][]string{}}
	if len(args) > 0 && jobs[args[0]] {
		c.Job, args = args[0], args[1:]
	}
	if compatJobs[c.Job] {
		c.Args = args
		return c, nil
	}
	p := parser{c: c, args: args, vars: variables{}}
	for p.i < len(p.args) {
		if err := p.next(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

type parser struct {
	c    *Command
	args []string
	i    int
	done bool // after "--", everything is an operand
	vars variables
}

func (p *parser) next() error {
	a := p.args[p.i]
	p.i++
	switch {
	case p.done || a == "-" || !strings.HasPrefix(a, "-"):
		p.c.Args = append(p.c.Args, a)
		return nil
	case a == "--":
		p.done = true
		return nil
	case strings.HasPrefix(a, "--"):
		return p.long(a)
	default:
		return p.shorts(a)
	}
}

// long reads --name, --name=value, --name value and --no-name.
func (p *parser) long(a string) error {
	name, value, hasValue := strings.Cut(a[2:], "=")
	name, expand := expandForm(name)
	o, negated := lookupLong(name)
	if o == nil {
		return unknown(a)
	}
	if err := p.inScope(o); err != nil {
		return err
	}
	if o.Type == typeBool {
		return p.setBool(o, negated, value, hasValue)
	}
	if o.Type == typeTopic && !negated {
		if !hasValue {
			value = "true" // --help alone; a topic comes after =
		}
		p.c.Values[o.Name] = append(p.c.Values[o.Name], value)
		return nil
	}
	if negated {
		return usagef("--%s is not an on/off option, so it has no --no- form", o.Name)
	}
	if !hasValue {
		if p.i >= len(p.args) {
			return usagef("--%s needs a value (%s)", o.Name, o.Type)
		}
		value = p.args[p.i]
		p.i++
	}
	if expand {
		var err error
		if value, err = p.vars.expand(value); err != nil {
			return usagef("--%s%s: %v", expandPrefix, o.Name, err)
		}
	}
	return p.set(o, value)
}

// expandForm reads --expand-NAME: it returns NAME and true when NAME is
// an option that takes a value, else the name as it is.
func expandForm(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, expandPrefix)
	if !ok || byName[name] != nil {
		return name, false
	}
	if o, negated := lookupLong(rest); o != nil && !negated && o.Type != typeBool && o.Type != typeTopic {
		return rest, true
	}
	return name, false
}

func (p *parser) setBool(o *Option, negated bool, value string, hasValue bool) error {
	on := !negated
	if hasValue {
		switch strings.ToLower(value) {
		case "true", "yes", "on", "1":
		case "false", "no", "off", "0":
			on = !on
		default:
			return usagef("--%s takes true or false, not %q", o.Name, value)
		}
	}
	p.c.Values[o.Name] = append(p.c.Values[o.Name], fmt.Sprint(on))
	return nil
}

// shorts reads a letter group such as -qo out or -n8. A letter on a
// boolean flips its default: -q turns quiet on, -k turns tls-verify off.
func (p *parser) shorts(a string) error {
	letters := a[1:]
	for j := 0; j < len(letters); j++ {
		o := lookupShort(letters[j])
		if o == nil {
			return unknown("-" + string(letters[j]))
		}
		if err := p.inScope(o); err != nil {
			return err
		}
		if o.Type == typeBool || o.Type == typeTopic {
			p.c.Values[o.Name] = append(p.c.Values[o.Name], fmt.Sprint(o.Default != "true"))
			continue
		}
		value := letters[j+1:]
		if value == "" {
			if p.i >= len(p.args) {
				return usagef("-%c (--%s) needs a value (%s)", o.Short, o.Name, o.Type)
			}
			value = p.args[p.i]
			p.i++
		}
		return p.set(o, value)
	}
	return nil
}

func (p *parser) set(o *Option, value string) error {
	if err := checkValue(o, value); err != nil {
		return &UsageError{Msg: err.Error()}
	}
	if o.Name == "variable" {
		if err := p.vars.set(value); err != nil {
			return &UsageError{Msg: err.Error()}
		}
	}
	p.c.Values[o.Name] = append(p.c.Values[o.Name], value)
	return nil
}

// inScope refuses an option outside the jobs it serves.
func (p *parser) inScope(o *Option) error {
	job := p.c.Job
	switch {
	case o.Group == "general":
		return nil
	case !transferJobs[job]:
		return usagef("--%s does not apply to ghola %s", o.Name, job)
	case o.Scope == "all" || o.Scope == job || (o.Scope == "fetch" && (job == JobFetch || job == JobInspect)):
		return nil
	case o.Scope == "fetch":
		return usagef("--%s works only when ghola fetches a URL (ghola URL), not with ghola %s", o.Name, job)
	default:
		return usagef("--%s works only with ghola %s", o.Name, o.Scope)
	}
}
