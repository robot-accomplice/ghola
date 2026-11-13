package client

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// errRangeIgnored reports a 200 answer to a Range request: the server sent
// the full body instead of the requested part.
var errRangeIgnored = errors.New("server ignored the Range request (status 200)")

// errUncheckedBody reports a Streamer that wrote body bytes without calling
// CheckHeaders first. It is a programming error in the Streamer.
var errUncheckedBody = errors.New("response body written before its headers were checked")

// checkedSink passes body bytes to w only after check accepts the response
// headers. Streamers call CheckHeaders through gholatransport.CopyBody.
type checkedSink struct {
	w       io.Writer
	check   func(gholatransport.StreamMeta) error
	checked bool
}

func newCheckedSink(w io.Writer, check func(gholatransport.StreamMeta) error) *checkedSink {
	return &checkedSink{w: w, check: check}
}

// CheckHeaders implements gholatransport.HeaderChecker.
func (s *checkedSink) CheckHeaders(meta gholatransport.StreamMeta) error {
	if err := s.check(meta); err != nil {
		return err
	}
	s.checked = true
	return nil
}

func (s *checkedSink) Write(p []byte) (int, error) {
	if !s.checked {
		return 0, errUncheckedBody
	}
	return s.w.Write(p)
}

// expectRange returns a check that accepts only a 206 response whose
// Content-Range starts at start and, when end >= 0, ends at end.
// A 200 response gives errRangeIgnored.
func expectRange(start, end int64) func(gholatransport.StreamMeta) error {
	return func(meta gholatransport.StreamMeta) error {
		switch meta.StatusCode {
		case fasthttp.StatusPartialContent:
		case fasthttp.StatusOK:
			return errRangeIgnored
		default:
			return fmt.Errorf("unexpected status %d for range %d-%s", meta.StatusCode, start, rangeEnd(end))
		}
		gotStart, gotEnd, ok := parseContentRange(meta.ContentRange)
		if !ok {
			return fmt.Errorf("bad Content-Range %q for range %d-%s", meta.ContentRange, start, rangeEnd(end))
		}
		if gotStart != start || (end >= 0 && gotEnd != end) {
			return fmt.Errorf("server sent bytes %d-%d, requested %d-%s", gotStart, gotEnd, start, rangeEnd(end))
		}
		return nil
	}
}

func rangeEnd(end int64) string {
	if end < 0 {
		return ""
	}
	return strconv.FormatInt(end, 10)
}

// parseContentRange parses "bytes START-END/TOTAL" (TOTAL may be "*").
func parseContentRange(v string) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(v), "bytes ")
	if !found {
		return 0, 0, false
	}
	span, _, found := strings.Cut(spec, "/")
	if !found {
		return 0, 0, false
	}
	a, b, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	start, err1 := strconv.ParseInt(a, 10, 64)
	end, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || start < 0 || end < start {
		return 0, 0, false
	}
	return start, end, true
}
