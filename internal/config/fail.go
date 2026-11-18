package config

import "errors"

// ErrHTTPStatus is returned when --fail is set and the server answers with
// a status that --fail refuses. The stdout path and the download path both
// return it, so both give the same exit code.
var ErrHTTPStatus = errors.New("non-2xx http status")

// FailsOn reports whether --fail refuses status. This is ghola's v0 rule:
// any status outside 2xx. curl -f refuses only 400 and above; the v1 flag
// design decides which rule v1 keeps.
func (o OutputOptions) FailsOn(status int) bool {
	return o.Fail && (status < 200 || status >= 300)
}
