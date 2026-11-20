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
