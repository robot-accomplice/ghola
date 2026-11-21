package config

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// EventLog writes --output-events json: one JSON object on each line, for
// each transfer event (spec decision J). docs/cli/output-events.schema.json
// defines the lines. A nil *EventLog writes nothing, so a caller does not
// check for it.
type EventLog struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

// NewEventLog returns the event log on w (stderr).
func NewEventLog(w io.Writer) *EventLog { return &EventLog{w: w, now: time.Now} }

// Event is one line. The event names are start, retry, done and error.
type Event struct {
	Event    string  `json:"event"`
	Time     string  `json:"time"` // RFC 3339, UTC, with milliseconds
	URL      string  `json:"url"`
	Attempt  int     `json:"attempt,omitempty"` // retry: the number of the retry
	Retries  int     `json:"retries,omitempty"` // retry: the --retry-max limit
	WaitMS   int64   `json:"wait_ms,omitempty"` // retry: the wait before it
	ExitCode *int    `json:"exit_code,omitempty"`
	Status   int     `json:"status,omitempty"` // done and error: the last HTTP status
	Bytes    int64   `json:"bytes,omitempty"`
	File     string  `json:"file,omitempty"`
	Seconds  float64 `json:"seconds,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// Start records that the transfer of url starts.
func (l *EventLog) Start(url string) { l.write(Event{Event: "start", URL: url}) }

// Retry records that retry number attempt of url starts after wait.
func (l *EventLog) Retry(url string, attempt, retries int, wait time.Duration) {
	l.write(Event{Event: "retry", URL: url, Attempt: attempt, Retries: retries, WaitMS: wait.Milliseconds()})
}

// End records the result of a transfer: done for exit code 0, else error.
func (l *EventLog) End(info TransferInfo) {
	e := Event{Event: "done", URL: info.URL, ExitCode: &info.ExitCode, Status: info.Status, Bytes: info.Size,
		File: info.File, Seconds: info.Time.Seconds(), Error: info.ErrorMsg}
	if info.ExitCode != NoError.Int() {
		e.Event = "error"
		if e.Error == "" {
			e.Error = fmt.Sprintf("exit code %d", info.ExitCode) // the schema requires a cause
		}
	}
	l.write(e)
}

func (l *EventLog) write(e Event) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Time = l.now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	b, _ := json.Marshal(e)    //nolint:errchkjson // the fields are plain
	l.w.Write(append(b, '\n')) //nolint:errcheck // an event that cannot be written must not stop the transfer
}
