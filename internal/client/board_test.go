package client

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// The board keeps one line for each job: a meter write replaces its own
// line only, and each draw after the first moves back to the first line,
// so the lines stay in place on the terminal.
func TestBoard(t *testing.T) {
	var out bytes.Buffer
	b := NewBoard(&out, 2)
	a, c := b.Line(0, "a.iso"), b.Line(1, "c.iso")
	fmt.Fprintf(a, "\r%6.1f%%  10/100 bytes  1.0 MB/s   ", 10.0)
	fmt.Fprintf(c, "\r 50.0%%  50/100 bytes  2.0 MB/s   ")
	fmt.Fprintf(a, "\r100.0%%  100/100 bytes  1.0 MB/s   \n")
	if b.lines[0] != "a.iso: 100.0%  100/100 bytes  1.0 MB/s" || b.lines[1] != "c.iso:  50.0%  50/100 bytes  2.0 MB/s" {
		t.Errorf("lines: %q", b.lines)
	}
	draws := strings.Split(out.String(), "\x1b[2A")
	if len(draws) != 3 {
		t.Fatalf("%d draws after the first; want 2:\n%q", len(draws)-1, out.String())
	}
	if last := draws[2]; strings.Count(last, "\n") != 2 || !strings.Contains(last, "c.iso:  50.0%") {
		t.Errorf("the last draw: %q", last)
	}
}
