package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// expandPrefix marks the form of an option that expands {{name}} in its
// value: --expand-header 'X: {{token}}' (spec decision I, curl's rule).
// The plain form never expands, so a body that holds {{ stays as it is.
const expandPrefix = "expand-"

// variableName is the syntax of a --variable name, as in curl.
var variableName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// variables are the --variable values. The parser sets them in command
// line order, so a variable must come before the option that uses it.
type variables map[string]string

// set reads one --variable: NAME=VALUE, NAME@FILE (@- is stdin), or
// %NAME, which takes the environment variable NAME (%NAME=DEFAULT gives a
// value when it is not set).
func (v variables) set(arg string) error {
	fromEnv := strings.HasPrefix(arg, "%")
	arg = strings.TrimPrefix(arg, "%")
	k := strings.IndexAny(arg, "=@")
	name, op, rest := arg, byte(0), ""
	if k >= 0 {
		name, op, rest = arg[:k], arg[k], arg[k+1:]
	}
	if !variableName.MatchString(name) {
		return fmt.Errorf("--variable %q: a name has only letters, digits and _ (use NAME=VALUE, NAME@FILE or %%NAME)", arg)
	}
	if fromEnv {
		if env, ok := os.LookupEnv(name); ok {
			v[name] = env
			return nil
		}
		if op == 0 {
			return fmt.Errorf("--variable %%%s: the environment variable %s is not set (give a default with %%%s=VALUE)", name, name, name)
		}
	}
	switch op {
	case '=':
		v[name] = rest
	case '@':
		b, err := readVariableFile(rest)
		if err != nil {
			return fmt.Errorf("--variable %s@%s: %w", name, rest, err)
		}
		v[name] = string(b)
	default:
		return fmt.Errorf("--variable %q needs a value (NAME=VALUE, NAME@FILE or %%NAME)", arg)
	}
	return nil
}

func readVariableFile(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// reference is {{name}} or {{name:fn:fn}}; \{{ is a literal {{.
var reference = regexp.MustCompile(`\\\{\{|\{\{([^{}]*)\}\}`)

// variableFuncs are curl's functions on a value.
var variableFuncs = map[string]func(string) string{
	"trim": strings.TrimSpace,
	"json": jsonString,
	"url":  url.QueryEscape,
	"b64":  func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
}

// jsonString is s as the inside of a JSON string, without HTML escapes.
func jsonString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s) //nolint:errcheck,errchkjson // a string always encodes
	out := strings.TrimSuffix(b.String(), "\n")
	return out[1 : len(out)-1]
}

// expand puts the variables into s. A name that is not set is an error,
// so a typo does not send an empty value.
func (v variables) expand(s string) (string, error) {
	var err error
	out := reference.ReplaceAllStringFunc(s, func(m string) string {
		if m == `\{{` {
			return "{{"
		}
		parts := strings.Split(m[2:len(m)-2], ":")
		value, ok := v[parts[0]]
		if !ok && err == nil {
			err = fmt.Errorf("{{%s}}: no such variable (set it first with --variable %s=VALUE)", parts[0], parts[0])
		}
		for _, fn := range parts[1:] {
			f := variableFuncs[fn]
			if f == nil {
				if err == nil {
					err = fmt.Errorf("{{%s}}: no function %q (use trim, json, url or b64)", m[2:len(m)-2], fn)
				}
				continue
			}
			value = f(value)
		}
		return value
	})
	return out, err
}
