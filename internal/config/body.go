// Package config — body.go builds request bodies for the curl-compatible
// data flags: --form (multipart), --data-urlencode.
package config

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"strings"
)

// URLEncodeData URL-encodes name=value pairs (the --data-urlencode flag) and
// joins them with '&'. A pair without '=' is encoded as a bare value.
func URLEncodeData(pairs []string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if i := strings.IndexByte(p, '='); i >= 0 {
			name, val := p[:i], p[i+1:]
			parts = append(parts, name+"="+url.QueryEscape(val))
		} else {
			parts = append(parts, url.QueryEscape(p))
		}
	}
	return strings.Join(parts, "&")
}

// BuildFormBody assembles a multipart/form-data body. Each entry is
// name=value, or name=@path to attach a file's contents. Returns the
// Content-Type (with boundary) and the encoded body.
func BuildFormBody(form []string) (string, []byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, entry := range form {
		i := strings.IndexByte(entry, '=')
		if i < 0 {
			return "", nil, fmt.Errorf("invalid --form %q (want name=value)", entry)
		}
		name, val := entry[:i], entry[i+1:]
		if strings.HasPrefix(val, "@") {
			path := val[1:]
			content, err := os.ReadFile(path)
			if err != nil {
				return "", nil, fmt.Errorf("read --form file %q: %w", path, err)
			}
			fw, err := mw.CreateFormFile(name, path)
			if err != nil {
				return "", nil, err
			}
			if _, err := fw.Write(content); err != nil {
				return "", nil, err
			}
		} else {
			if err := mw.WriteField(name, val); err != nil {
				return "", nil, err
			}
		}
	}
	if err := mw.Close(); err != nil {
		return "", nil, err
	}
	return mw.FormDataContentType(), buf.Bytes(), nil
}
