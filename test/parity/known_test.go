//go:build parity

package parity

// known lists the scenarios that ghola fails today, with the finding that
// explains each failure. The list is a ratchet:
//
//   - A scenario in this list that ghola now passes fails the test. Remove
//     it from the list in the same change as the fix.
//   - A scenario that ghola fails and that is not in this list fails the test.
//
// So a fix cannot land without proof, and a regression cannot land at all.
// The findings are in docs/audits/2026-11-12-v0.7.0-vs-curl-aria2-wget.md.
// Recorded against v0.8.0 on 2026-11-15.
var known = map[string]string{}

// compatKnown lists the scenarios that a compatibility command fails
// while its tool passes, by "scenario command", with the reason. Keep an
// entry only where the tool passes by a quirk that ghola must not copy.
var compatKnown = map[string]string{
	// Wget2 2.1 does not resume with -c -O: it sends Range: bytes=0- and
	// gets a whole new copy (parity records of PR 82, ubuntu-24.04). GNU
	// wget, curl and aria2 resume from the partial file, as ghola wget
	// does, and without a record of the old version none of them can tell
	// that the file changed. Native ghola keeps that record (If-Range).
	"resume_changed ghola-wget": "Wget2 2.1 restarts instead of resuming",
}
