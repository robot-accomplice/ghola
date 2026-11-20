package netrc

import "testing"

// Each case is a netrc file and the user:password that curl 8.18.0 sent to
// a local server for it on 2026-11-20 (curl --netrc-file F URL).
func TestFind_MatchesCurl(t *testing.T) {
	cases := []struct {
		name, netrc, user, want string
	}{
		{"one line", "machine 127.0.0.1 login alice password s3cret\n", "", "alice:s3cret"},
		{"default entry", "machine other login x password y\ndefault login dflt password dp\n", "", "dflt:dp"},
		{"quoted password", "machine 127.0.0.1\n  login bob\n  password \"a b\\\"c\"\n", "", "bob:a b\"c"},
		{"macdef body skipped", "macdef init\nmachine 127.0.0.1 login evil password evil\n\nmachine 127.0.0.1 login good password g\n", "", "good:g"},
		{"first match", "machine 127.0.0.1 login a1 password p1\nmachine 127.0.0.1 login a2 password p2\n", "", "a1:p1"},
		{"upper-case keywords", "MACHINE 127.0.0.1 LOGIN up PASSWORD per\n", "", "up:per"},
		{"comments", "# comment\nmachine 127.0.0.1 login c password d # trailing\n", "", "c:d"},
		{"login without password", "machine 127.0.0.1 login only\n", "", "only:"},
		{"URL user picks its entry", "machine 127.0.0.1 login a1 password p1\nmachine 127.0.0.1 login a2 password p2\n", "a2", "a2:p2"},
	}
	for _, c := range cases {
		entries, err := Parse(c.netrc)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		e, ok := Find(entries, "127.0.0.1", c.user)
		if got := e.Login + ":" + e.Password; !ok || got != c.want {
			t.Errorf("%s: got %q (found %v), want %q", c.name, got, ok, c.want)
		}
	}
}

// A host with no entry and no default has no credentials.
func TestFind_NoEntry(t *testing.T) {
	entries, _ := Parse("machine other login x password y\n")
	if _, ok := Find(entries, "127.0.0.1", ""); ok {
		t.Error("found credentials for a host with no entry")
	}
}

// A quote that is not closed is an error that names the line, so a broken
// file does not send a wrong password.
func TestParse_OpenQuote(t *testing.T) {
	if _, err := Parse("machine h\nlogin u password \"abc\n"); err == nil || err.Error() != "netrc line 2: a quote is not closed" {
		t.Fatalf("err = %v", err)
	}
}
