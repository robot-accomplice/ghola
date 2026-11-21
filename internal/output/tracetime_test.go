package output

import (
	"bytes"
	"regexp"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// --trace-time puts the time in front of each -v line, as curl does, so
// the lines can be matched with a trace.
func TestProcessResponse_TraceTimeTimesVerbose(t *testing.T) {
	req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetBodyString("x")
	for timed, want := range map[bool]string{true: `^\d\d:\d\d:\d\d\.\d{6} Total: `, false: `^Total: `} {
		var out, errOut bytes.Buffer
		opts := &config.Options{Output: config.OutputOptions{Verbose: true, TraceTime: timed}}
		if err := ProcessResponse(&out, &errOut, opts, req, rsp, time.Second); err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(want).MatchString(errOut.String()) {
			t.Errorf("--trace-time %v: stderr %q", timed, errOut.String())
		}
	}
}
