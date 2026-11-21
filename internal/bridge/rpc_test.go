package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

// rpcCall sends one JSON-RPC request to the handler and returns the
// result, or the error message.
func rpcCall(t *testing.T, h fasthttp.RequestHandler, method string, params any) (json.RawMessage, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetRequestURI(RPCPath)
	ctx.Request.SetBody(body)
	h(ctx)
	var rsp struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(ctx.Response.Body(), &rsp); err != nil {
		t.Fatalf("%s: %v: %s", method, err, ctx.Response.Body())
	}
	if rsp.Error != nil {
		return nil, rsp.Error.Message
	}
	return rsp.Result, ""
}

// waitStatus polls ghola.list until the job has the status.
func waitStatus(t *testing.T, h fasthttp.RequestHandler, gid, want string) rpcJob {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		res, _ := rpcCall(t, h, "ghola.list", nil)
		var jobs []rpcJob
		json.Unmarshal(res, &jobs) //nolint:errcheck
		for _, j := range jobs {
			if j.GID == gid && j.Status == want {
				return j
			}
		}
	}
	t.Fatalf("job %s never became %s", gid, want)
	return rpcJob{}
}

// The JSON-RPC endpoint adds download jobs and lists them, as aria2's RPC
// does. A job may pause and resume (with -c), and stop. A file:// URL and
// an option that runs a command or reads a local file are refused, so a
// client with the token cannot do more than send requests.
func TestRPC(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, "content of "+r.URL.Path)
	}))
	defer srv.Close()
	h := Handler(nil)
	dir := t.TempDir() + "/"
	res, msg := rpcCall(t, h, "ghola.add", map[string]any{"urls": []string{srv.URL + "/a"}, "args": []string{"-o", dir}})
	if msg != "" {
		t.Fatal(msg)
	}
	var gids []string
	json.Unmarshal(res, &gids) //nolint:errcheck
	job := waitStatus(t, h, gids[0], "complete")
	if b, _ := os.ReadFile(filepath.Join(dir, "a")); string(b) != "content of /a" || job.Bytes != int64(len(b)) {
		t.Errorf("file %q, bytes %d", b, job.Bytes)
	}

	res, _ = rpcCall(t, h, "ghola.add", map[string]any{"urls": []string{srv.URL + "/slow"}, "args": []string{"-o", dir, "-r", "0"}})
	json.Unmarshal(res, &gids) //nolint:errcheck
	slow := gids[0]
	waitStatus(t, h, slow, "active")
	if _, msg := rpcCall(t, h, "ghola.pause", map[string]any{"gid": slow}); msg != "" {
		t.Fatal(msg)
	}
	waitStatus(t, h, slow, "paused")
	close(release)
	if _, msg := rpcCall(t, h, "ghola.resume", map[string]any{"gid": slow}); msg != "" {
		t.Fatal(msg)
	}
	waitStatus(t, h, slow, "complete")
	if n, _ := rpcCall(t, h, "ghola.purge", nil); string(n) != "2" {
		t.Errorf("purge removed %s jobs, want 2", n)
	}

	for name, params := range map[string]any{
		"file URL":    map[string]any{"urls": []string{"file:///etc/passwd"}},
		"a command":   map[string]any{"urls": []string{srv.URL}, "args": []string{"--on-complete", "calc"}},
		"a config":    map[string]any{"urls": []string{srv.URL}, "args": []string{"--config", "x"}},
		"no urls":     map[string]any{},
		"url in args": map[string]any{"urls": []string{srv.URL}, "args": []string{srv.URL + "/b"}},
	} {
		if _, msg := rpcCall(t, h, "ghola.add", params); msg == "" {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, msg := rpcCall(t, h, "aria2.addUri", nil); !strings.Contains(msg, "ghola.add") {
		t.Errorf("an unknown method: %q", msg)
	}
}
