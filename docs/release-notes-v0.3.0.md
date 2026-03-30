# Release Notes Draft

## Browser-Like Fetching

This release adds a pure-Go browser impersonation path to Ghola while keeping the tool focused on command-line URL fetching rather than scraping or browser automation.

Highlights:

- `--impersonate` for browser-like Chrome, Firefox, Safari, and Edge-family profiles
- pure-Go TLS, HTTP/2, and optional HTTP/3 impersonation backend
- coherent browser-style request headers
- persistent cookie jars
- proxy selection and proxy pools
- bridge support for the new fetch options

## Attribution

The direction for this feature family was inspired by ideas demonstrated in the Scrapling project by Karim Shoair. Ghola adapts those ideas to a narrower fetcher-only tool and does not implement Scrapling's scraping or automation features.

## Operator Notes

- `edge` currently uses a Chrome-family impersonation backend profile as the nearest available pure-Go approximation.
- `--http3` is only available when impersonation is enabled.
- `--ghost` remains opt-in and is intended for traceability, not stealth.

## Verification

```bash
GOCACHE=/Users/jmachen/code/ghola/.gocache go test ./...
```
