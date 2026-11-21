package config

import (
	"bytes"
	"testing"
)

// The escapes and %% become their characters, %{stderr} switches the
// output, and an unknown variable or an open %{ is an error, because the
// output would then be wrong without a sign.
func TestTemplate(t *testing.T) {
	tmpl, err := ParseTemplate(`100%% %{http_code}\t%{stderr}%{response_code}\n`)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := tmpl.Write(&out, &errOut, TransferInfo{Status: 7}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "100% 007\t" || errOut.String() != "7\n" {
		t.Errorf("stdout %q, stderr %q", out.String(), errOut.String())
	}
	for _, bad := range []string{"%{no_such_var}", "%{http_code"} {
		if _, err := ParseTemplate(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
