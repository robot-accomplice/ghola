package config

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"
)

// --form name=@file;type=T;filename=N sends the file with that type and
// name, as curl -F does. The file name is the base name, not the path.
// Before, ";type=text/plain" was read as part of the path (exit 26).
func TestBuildFormBody_FileParameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.bin")
	if err := os.WriteFile(path, []byte("XBIN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for entry, want := range map[string][2]string{
		"f=@" + path:                              {"x.bin", "application/octet-stream"},
		"f=@" + path + ";type=text/plain":         {"x.bin", "text/plain"},
		"f=@" + path + ";filename=a.txt;type=t/x": {"a.txt", "t/x"},
	} {
		ct, body, err := BuildFormBody([]string{entry})
		if err != nil {
			t.Fatalf("%s: %v", entry, err)
		}
		_, params, _ := mime.ParseMediaType(ct)
		part, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).NextPart()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(part)
		if part.FormName() != "f" || part.FileName() != want[0] || part.Header.Get("Content-Type") != want[1] || string(data) != "XBIN\n" {
			t.Errorf("%s: name %q, file %q, type %q, data %q", entry, part.FormName(), part.FileName(), part.Header.Get("Content-Type"), data)
		}
	}
}
