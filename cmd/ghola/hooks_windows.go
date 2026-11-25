package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// quoteForBatch makes a .bat or .cmd hook safe to run. Windows runs a batch
// file through cmd.exe, which reads the command line again: "&", "|", "<",
// ">", "^" and "(" in an unquoted argument start new commands. Go quotes an
// argument only when it holds a space or a quote, so a server-chosen file
// name such as "a&cmd&.bin" ran cmd. ghola starts cmd.exe itself, with
// every argument in quotes. Inside quotes cmd.exe still expands %VAR% and
// ends the quote at ", so an argument with "%", '"' or a control
// character is refused.
func quoteForBatch(cmd *exec.Cmd) error {
	ext := strings.ToLower(filepath.Ext(cmd.Path))
	if ext != ".bat" && ext != ".cmd" {
		return nil
	}
	parts := make([]string, 0, len(cmd.Args))
	parts = append(parts, `"`+cmd.Path+`"`)
	for _, a := range cmd.Args[1:] {
		if strings.ContainsAny(a, `%"`) || strings.IndexFunc(a, func(r rune) bool { return r < 0x20 }) >= 0 {
			return fmt.Errorf("the argument %q holds a character that cmd.exe would expand", a)
		}
		parts = append(parts, `"`+a+`"`)
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		return errors.New("ComSpec is not set, so cmd.exe cannot be found")
	}
	cmd.Path = comspec
	cmd.Args = nil
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// /d: no AutoRun commands. /v:off: no !VAR! expansion. /s /c: remove only
	// the outer quotes of the rest of the line, then run it.
	cmd.SysProcAttr.CmdLine = `"` + comspec + `" /d /v:off /s /c "` + strings.Join(parts, " ") + `"`
	return nil
}
