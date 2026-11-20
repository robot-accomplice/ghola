package cli

// SplitNext splits a command line at --next, as curl -: / --next does:
// each part holds the options and the URLs of one group, and the groups
// run in order. After "--" every argument is a URL, so --next there is
// one too. --next is for fetch only (v1 spec, request group). A
// compatibility command keeps its arguments whole: they are that tool's
// syntax.
func SplitNext(args []string) ([][]string, error) {
	if len(args) > 0 && jobs[args[0]] {
		if compatJobs[args[0]] {
			return [][]string{args}, nil
		}
		for _, a := range args[1:] {
			if a == "--" {
				break
			}
			if a == "--next" {
				return nil, usagef("--next is for fetch only, not ghola %s", args[0])
			}
		}
		return [][]string{args}, nil
	}
	var groups [][]string
	cur := []string{}
	for i, a := range args {
		if a == "--" {
			cur = append(cur, args[i:]...)
			break
		}
		if a == "--next" {
			groups = append(groups, cur)
			cur = []string{}
			continue
		}
		cur = append(cur, a)
	}
	return append(groups, cur), nil
}
