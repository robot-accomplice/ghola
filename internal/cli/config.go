package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultConfigPath is ~/.config/ghola/config ($XDG_CONFIG_HOME when set),
// or %APPDATA%\ghola\config on Windows (v1 spec, section 8.2). It is ""
// when the folder is not known.
func DefaultConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if runtime.GOOS == "windows" {
		dir = os.Getenv("APPDATA")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ghola", "config")
}

// WithConfig returns the command with the options of the config file in
// front of the command line's, so that the command line wins: a single
// option takes its last value, and a repeated option (-H) gets both.
// args is the command line that gave c. --config PATH reads another file,
// and --no-config reads none. A missing default file is no error. The
// compatibility commands read no ghola config: curl, wget and aria2c have
// config rules of their own.
func WithConfig(args []string, c *Command) (*Command, error) {
	// Only the transfer jobs read the config: a bad line must not stop
	// --help, --version, explain or completion, which can say what is wrong.
	if !transferJobs[c.Job] || c.Has("help") || c.Has("version") || c.Last("no-config") == "true" {
		return c, nil
	}
	path, explicit := c.Last("config"), true
	if path == "" {
		path, explicit = DefaultConfigPath(), false
	}
	if path == "" {
		return c, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return c, nil
	}
	if err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("--config: %v", err)}
	}
	opts, err := configArgs(c.Job, path, string(data))
	if err != nil {
		return nil, err
	}
	if len(opts) == 0 {
		return c, nil
	}
	rest := args
	if c.Job != "" {
		rest = args[1:] // Parse took the job word from args[0]
	}
	all := append(opts, rest...)
	if c.Job != "" {
		all = append([]string{c.Job}, all...)
	}
	return Parse(all)
}

// configArgs turns the lines of a config file into --name=value
// arguments. A line is "name = value", or "name" for an on/off option;
// # starts a comment line. A known option outside the job is skipped, so
// one file serves every job. An unknown option is an error with its line.
func configArgs(job, path, text string) ([]string, error) {
	var out []string
	scope := parser{c: &Command{Job: job}}
	text = strings.TrimPrefix(text, "\ufeff") // a BOM that an editor saved
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, hasValue := strings.Cut(line, "=")
		name = strings.TrimPrefix(strings.TrimSpace(name), "--")
		value = configValue(value)
		o, _ := lookupLong(name)
		switch {
		case o == nil:
			return nil, usagef("config file %s, line %d: %q is not a ghola option", path, n+1, name)
		case o.Name == "config" || o.Name == "no-config":
			return nil, usagef("config file %s, line %d: --%s works only on the command line", path, n+1, o.Name)
		case scope.inScope(o) != nil:
			continue
		}
		arg := "--" + name
		if hasValue {
			arg += "=" + value
		}
		out = append(out, arg)
	}
	check := out
	if job != "" {
		check = append([]string{job}, out...)
	}
	if _, err := Parse(check); err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	return out, nil
}

// configValue is the value of a config line. A quoted value is taken as
// it is; in an unquoted one, whitespace and then # start a comment, so a
// URL keeps its #fragment.
func configValue(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}
