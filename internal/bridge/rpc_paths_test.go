package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An RPC client with the token may download, but only into the folder
// where ghola serve runs. Writing any other path would let it replace a
// file anywhere the user can write, for example a start-up script.
func TestRPC_OutputStaysInTheServeFolder(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	outside := filepath.Join(filepath.Dir(root), "outside.bin")
	h := Handler(nil)
	for name, args := range map[string][]string{
		"absolute path outside": {"-o", outside},
		"climbs out":            {"-o", "a/../../escape.bin"},
		"overwrite":             {"-o", "f.bin", "--output-overwrite"},
	} {
		if _, msg := rpcCall(t, h, "ghola.add", map[string]any{"urls": []string{"http://127.0.0.1:1/x"}, "args": args}); msg == "" {
			t.Errorf("%s: ghola.add accepted %q", name, args)
		}
	}
	for _, args := range [][]string{{"-o", "sub/"}, {"-o", filepath.Join(root, "in.bin")}} {
		if _, msg := rpcCall(t, h, "ghola.add", map[string]any{"urls": []string{"http://127.0.0.1:1/x"}, "args": append(args, "-r", "0")}); msg != "" {
			t.Errorf("%q inside the folder: %s", args, msg)
		}
	}
}

// --source names another mirror of the file. Over RPC it must be http(s),
// as the URLs are: a file:// source would copy a local file into the job.
func TestRPC_SourceMustBeHTTP(t *testing.T) {
	t.Chdir(t.TempDir())
	h := Handler(nil)
	local := "file:///" + strings.TrimPrefix(filepath.ToSlash(os.Args[0]), "/")
	for _, src := range []string{local, "ftp://127.0.0.1/x", "sftp://127.0.0.1/x"} {
		args := []string{"--source", src, "-o", "s.bin"}
		if _, msg := rpcCall(t, h, "ghola.add", map[string]any{"urls": []string{"http://127.0.0.1:1/x"}, "args": args}); msg == "" {
			t.Errorf("ghola.add accepted --source %s", src)
		}
	}
}
