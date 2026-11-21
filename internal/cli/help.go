package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// groupTitles orders the --help sections, as the spec does.
var groupTitles = []struct{ group, title string }{
	{"output", "Output"}, {"request", "Request"}, {"http", "HTTP"}, {"limits", "Time and speed limits"},
	{"retry", "Retries"}, {"net", "Names and connections"}, {"tls", "TLS"}, {"auth", "Authentication"},
	{"proxy", "Proxies"}, {"cookie", "Cookies"}, {"impersonate", "Impersonation"},
	{"download", "Download manager (ghola download)"}, {"mirror", "Mirror (ghola mirror)"}, {"general", "General"},
}

// ShortUsage is the summary that an argument error shows: the jobs and the
// options that have a letter. --help shows every option.
func ShortUsage(version string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ghola %s\n", version)
	b.WriteString("Usage: ghola [options] URL...      fetch\n")
	b.WriteString("       ghola download [options] URL...\n")
	b.WriteString("       ghola serve | profiles | explain CODE\n")
	b.WriteString("       ghola curl|wget|aria2c ARGS  (the syntax of that tool)\n\n")
	var lettered []*Option
	for i := range table {
		if table[i].Short != 0 {
			lettered = append(lettered, &table[i])
		}
	}
	sort.Slice(lettered, func(i, j int) bool { return lettered[i].Short < lettered[j].Short })
	for _, o := range lettered {
		fmt.Fprintf(&b, "  -%c, --%-18s %s\n", o.Short, o.Name, o.Help)
	}
	b.WriteString("\nghola --help lists every option; ghola --help=GROUP lists one group (tls, proxy, ...).\n")
	return b.String()
}

// Help lists the options: all of them, or one group. Options of a phase
// that is not built yet are marked.
func Help(version, group string) (string, error) {
	var b strings.Builder
	b.WriteString(ShortUsage(version))
	found := false
	for _, g := range groupTitles {
		if group != "" && group != "true" && g.group != group {
			continue
		}
		found = true
		fmt.Fprintf(&b, "\n%s:\n", g.title)
		for i := range table {
			if o := &table[i]; o.Group == g.group {
				b.WriteString(helpLine(o))
			}
		}
	}
	if !found {
		return "", usagef("--help=%s: no such group (use one of: %s)", group, groupNames())
	}
	return b.String(), nil
}

func helpLine(o *Option) string {
	name := "    --" + o.Name
	if o.Short != 0 {
		name = fmt.Sprintf("-%c, --%s", o.Short, o.Name)
	}
	if o.Type != typeBool && o.Type != typeTopic {
		name += " " + strings.ToUpper(strings.Fields(o.Type)[0])
	}
	note := ""
	if _, built := setters[o.Name]; !built && !callerOptions[o.Name] {
		note = fmt.Sprintf(" [phase %s, not built yet]", o.Phase)
	}
	return fmt.Sprintf("  %-34s %s%s\n", name, o.Help, note)
}

func groupNames() string {
	names := make([]string, len(groupTitles))
	for i, g := range groupTitles {
		names[i] = g.group
	}
	return strings.Join(names, ", ")
}

// exitCodes are curl's codes that ghola uses, with the cause and what to
// try (ghola explain CODE). The README has the same table.
const checkThePath = "Check the path."

var exitCodes = map[int][2]string{
	0:  {"Success.", ""},
	1:  {"A redirect went to a protocol that ghola does not follow, for example file://.", "Fetch that URL yourself if you trust it."},
	2:  {"An argument is wrong or missing.", "ghola --help lists the options; the message names the argument."},
	3:  {"A URL glob ({a,b} or [1-9]) is not valid.", `Escape { and [ with \, or use --no-url-glob.`},
	6:  {"The host name did not resolve.", "Check the name, your DNS, or the proxy settings."},
	7:  {"The connection failed, for example it was refused.", "Check the host and the port, and that the server runs."},
	22: {"The server answered with status 400 or above.", "--no-fail writes the answer and exits 0."},
	23: {"ghola could not write the output (stdout or the -o file).", "Check the folder, the free space, or a closed pipe."},
	26: {"ghola could not read a local file (-d @file, --upload, --form name=@file).", checkThePath},
	28: {"A time limit ended the transfer (--timeout, --timeout-connect, --stall-time).", "Raise the limit, or check the network."},
	35: {"The TLS handshake failed.", "Check --tls-min and the server's TLS setup."},
	37: {"ghola could not read the file of a file:// URL.", checkThePath},
	47: {"Redirects reached --follow-max.", "Raise --follow-max, or check for a redirect loop."},
	55: {"The request failed for another cause.", "ghola -v shows the steps."},
	56: {"The connection failed after it was ready, for example a reset.", "Retry; the server or a middlebox closed the connection."},
	60: {"The server certificate did not verify, or the SSH host key is not in ~/.ssh/known_hosts.", "Add the CA with --tls-ca, or the host key to known_hosts; -k skips the check."},
	32: {"The body does not match --checksum, or the server's Content-Digest.", "Check the digest; a download that ghola checks keeps its file."},
	63: {"The body is larger than --max-size.", "Raise --max-size, or check the URL."},
	90: {"The server public key does not match --tls-pin.", "Check the pin; the server key may have changed."},
	91: {"The server sent no good OCSP staple (--tls-ocsp).", "Ask the server owner to staple OCSP, or run without --tls-ocsp."},
	61: {"--compressed got a Content-Encoding that ghola cannot decode.", "--no-compressed gets the body as it is."},
	67: {"The server refused the login (FTP, SFTP, SCP).", "Check the user and the password (-u or the URL)."},
	78: {"The remote file does not exist (FTP, SFTP, SCP).", checkThePath},
}

// Explain describes an exit code, for ghola explain CODE.
func Explain(arg string) (string, error) {
	code, err := strconv.Atoi(arg)
	if err != nil {
		return "", usagef("ghola explain takes an exit code, such as 28")
	}
	e, ok := exitCodes[code]
	if !ok {
		return "", usagef("ghola does not use exit code %d", code)
	}
	if e[1] == "" {
		return fmt.Sprintf("%d: %s\n", code, e[0]), nil
	}
	return fmt.Sprintf("%d: %s\nTry: %s\n", code, e[0], e[1]), nil
}
