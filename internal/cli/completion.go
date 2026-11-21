package cli

import (
	"fmt"
	"sort"
	"strings"
)

// The shells that ghola completion writes a script for (v1 spec, section
// 1). The scripts come from the option table, so a new option completes
// as soon as it is in docs/cli/v1-options.csv.
var completionShells = map[string]func() string{
	"bash":       bashCompletion,
	"zsh":        zshCompletion,
	"fish":       fishCompletion,
	"powershell": powershellCompletion,
}

// Completion returns the completion script for a shell.
func Completion(shell string) (string, error) {
	gen, ok := completionShells[shell]
	if !ok {
		names := make([]string, 0, len(completionShells))
		for n := range completionShells {
			names = append(names, n)
		}
		sort.Strings(names)
		return "", usagef("ghola completion: no script for %q (use one of: %s)", shell, strings.Join(names, ", "))
	}
	return gen(), nil
}

// completionWords are the jobs and every long option, with the --no- form
// of each boolean.
func completionWords() (jobWords, options []string) {
	for j := range jobs {
		jobWords = append(jobWords, j)
	}
	sort.Strings(jobWords)
	for _, o := range table {
		if strings.HasSuffix(o.Name, "*") {
			continue // proxy-tls-* stands for a family
		}
		options = append(options, "--"+o.Name)
		if o.Type == typeBool {
			options = append(options, "--no-"+o.Name)
		}
	}
	return jobWords, options
}

func bashCompletion() string {
	jobWords, options := completionWords()
	return fmt.Sprintf(`# bash completion for ghola. Load it with: source <(ghola completion bash)
_ghola() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    if [[ $COMP_CWORD -eq 1 && $cur != -* ]]; then
        COMPREPLY=($(compgen -W "%s" -- "$cur"))
        return
    fi
    if [[ $cur == -* ]]; then
        COMPREPLY=($(compgen -W "%s" -- "$cur"))
    fi
}
complete -o default -F _ghola ghola
`, strings.Join(jobWords, " "), strings.Join(options, " "))
}

func zshCompletion() string {
	jobWords, options := completionWords()
	return fmt.Sprintf(`#compdef ghola
# zsh completion for ghola. Put it in a file named _ghola on $fpath.
_ghola() {
    if (( CURRENT == 2 )) && [[ $words[2] != -* ]]; then
        compadd -- %s
        return
    fi
    compadd -- %s
    _files
}
_ghola "$@"
`, strings.Join(jobWords, " "), strings.Join(options, " "))
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# fish completion for ghola. Load it with: ghola completion fish | source\n")
	jobWords, _ := completionWords()
	fmt.Fprintf(&b, "complete -c ghola -n '__fish_use_subcommand' -a '%s'\n", strings.Join(jobWords, " "))
	for _, o := range table {
		if strings.HasSuffix(o.Name, "*") {
			continue
		}
		line := "complete -c ghola -l " + o.Name
		if o.Short != 0 {
			line += " -s " + string(o.Short)
		}
		if o.Type != typeBool && o.Type != typeTopic {
			line += " -r"
		}
		fmt.Fprintf(&b, "%s -d %s\n", line, fishQuote(o.Help))
	}
	return b.String()
}

func fishQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

func powershellCompletion() string {
	jobWords, options := completionWords()
	quote := func(ws []string) string {
		q := make([]string, len(ws))
		for i, w := range ws {
			q[i] = "'" + w + "'"
		}
		return strings.Join(q, ",")
	}
	return fmt.Sprintf(`# PowerShell completion for ghola. Load it with:
#   ghola completion powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName ghola -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $jobs = @(%s)
    $options = @(%s)
    $words = if ($commandAst.CommandElements.Count -le 2 -and -not $wordToComplete.StartsWith('-')) { $jobs } else { $options }
    $words | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
`, quote(jobWords), quote(options))
}
