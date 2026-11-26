// Package config — body.go builds request bodies for the curl-compatible
// data flags: --form (multipart), --data-urlencode.
package config

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
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
// name=value, or name=@path to attach a file's contents. As in curl -F, a
// file can end in ;type=TYPE and ;filename=NAME. Returns the
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
			if err := writeFormFile(mw, name, val[1:]); err != nil {
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

// writeFormFile writes one file part. spec is PATH[;type=TYPE][;filename=NAME].
// The part names the file by its base name, as curl does, and its type
// is application/octet-stream unless ;type= gives one.
func writeFormFile(mw *multipart.Writer, name, spec string) error {
	params := strings.Split(spec, ";")
	path, fileName, fileType := params[0], filepath.Base(params[0]), "application/octet-stream"
	for _, p := range params[1:] {
		k, v, _ := strings.Cut(p, "=")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "type":
			fileType = v
		case "filename":
			fileName = v
		default:
			return fmt.Errorf("--form %s=@%s: %q is not a file parameter (use ;type= or ;filename=)", name, spec, p)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read --form file %q: %w", path, err)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, quoteEscaper.Replace(name), quoteEscaper.Replace(fileName)))
	h.Set("Content-Type", fileType)
	fw, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = fw.Write(content)
	return err
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")
