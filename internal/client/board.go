package client

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// MaxBoardLines is the most jobs that the board shows; above it the
// lines would not fit a terminal, and the meters stay off.
const MaxBoardLines = 40

// Board shows one line for each job of --jobs on a terminal (plan section
// 6: aria2 shows one summary line, wget one job at a time). Each job's
// meter writes to its own line, and the board draws all lines again in
// place, with ANSI cursor moves.
type Board struct {
	mu    sync.Mutex
	w     io.Writer
	lines []string
	drawn bool
}

// NewBoard returns a board of n lines on w.
func NewBoard(w io.Writer, n int) *Board {
	return &Board{w: w, lines: make([]string, n)}
}

// Line returns the writer of line i, which shows name and the meter.
func (b *Board) Line(i int, name string) io.Writer {
	b.mu.Lock()
	b.lines[i] = name + ": waiting"
	b.mu.Unlock()
	return boardLine{b: b, i: i, name: name}
}

type boardLine struct {
	b    *Board
	i    int
	name string
}

// Write takes the text after the last \r of the meter as the line.
func (l boardLine) Write(p []byte) (int, error) {
	s := string(p)
	if k := strings.LastIndexByte(s, '\r'); k >= 0 {
		s = s[k+1:]
	}
	if s = strings.TrimRight(s, "\r\n "); s != "" {
		l.b.set(l.i, l.name+": "+s)
	}
	return len(p), nil
}

func (b *Board) set(i int, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines[i] = text
	var out strings.Builder
	if b.drawn {
		fmt.Fprintf(&out, "\x1b[%dA", len(b.lines)) // back to the first line
	}
	for _, line := range b.lines {
		out.WriteString("\r\x1b[2K" + line + "\n")
	}
	b.drawn = true
	io.WriteString(b.w, out.String()) //nolint:errcheck // a meter that cannot draw must not stop the job
}
