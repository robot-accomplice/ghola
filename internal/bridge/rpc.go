package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"

	"github.com/fasthttp/websocket"
	"github.com/valyala/fasthttp"

	"github.com/robot-accomplice/ghola/internal/cli"
	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
)

// RPCPath is the JSON-RPC 2.0 endpoint of the bridge (plan phase 3, item
// 9): it adds, lists, pauses, resumes and stops download jobs, as aria2's
// RPC does. It sits behind the same Host, token and Content-Type checks as
// the bridge, on the same listener. It takes HTTP POST and WebSocket
// (spec decision M). A WebSocket client also gets the notifications
// ghola.onComplete and ghola.onError, with params [{"gid": ...}], as
// aria2's onDownloadComplete and onDownloadError.
const RPCPath = "/jsonrpc"

// maxRPCMessage is the largest WebSocket message that the RPC reads.
const maxRPCMessage = 1 << 20

// The notification methods.
const (
	rpcOnComplete = "ghola.onComplete"
	rpcOnError    = "ghola.onError"
)

// upgrader keeps the default origin check: a request with an Origin of
// another host is refused. Guard checks the token first, which a browser
// cannot send on a WebSocket.
var upgrader = websocket.FastHTTPUpgrader{}

// rpcOptions are the options that an RPC job may set. A client with the
// token may send requests already, but not run a command (--on-complete),
// read a local file (--config, --input) or write logs and traces.
var rpcOptions = map[string]bool{
	"output": true, "output-name": true, "connections": true, "checksum": true, "header": true,
	"user-agent": true, "referer": true, "rate-max": true, "retry-max": true, "retry-delay": true,
	"timeout": true, "timeout-connect": true, "source": true, "follow-max": true, "impersonate": true,
	"output-overwrite": true, "max-size": true, "cookie": true,
}

// rpcJob is one download that the RPC started.
type rpcJob struct {
	GID    string `json:"gid"`
	URL    string `json:"url"`
	File   string `json:"file"`
	Status string `json:"status"` // active, paused, complete, error, removed
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error,omitempty"`

	args   []string
	cancel context.CancelFunc
	// run counts the starts. Only the newest run may set the status: a
	// paused run can end after its resume has started.
	run  int
	info *config.TransferInfo
}

// rpcServer holds the jobs of one bridge.
type rpcServer struct {
	mu      sync.Mutex
	jobs    map[string]*rpcJob
	stream  client.Streamer
	clients map[*wsClient]bool // the WebSocket clients, for notifications
}

func newRPC(stream client.Streamer) *rpcServer {
	return &rpcServer{jobs: map[string]*rpcJob{}, stream: stream, clients: map[*wsClient]bool{}}
}

// wsClient is one WebSocket connection. A connection takes one writer at
// a time, so the answers and the notifications share a lock.
type wsClient struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (c *wsClient) send(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

// reply answers one request. It holds the write lock from the call to
// the answer, so a notification that the call causes (a ghola.add whose
// download ends at once) comes after the answer that names its job. The
// lock order is c.mu, then s.mu; notify never holds s.mu while it waits
// for c.mu.
func (c *wsClient) reply(s *rpcServer, msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, s.answer(msg))
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC 2.0 error codes.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcNoMethod       = -32601
	rpcInvalidParams  = -32602
)

// serve answers one JSON-RPC request: a WebSocket upgrade or a POST.
func (s *rpcServer) serve(ctx *fasthttp.RequestCtx) {
	if websocket.FastHTTPIsWebSocketUpgrade(ctx) {
		s.serveWS(ctx)
		return
	}
	ctx.SetContentType("application/json")
	ctx.SetBody(s.answer(ctx.PostBody()))
}

// serveWS reads one JSON-RPC request from each message and writes the
// answer, until the client closes the connection.
func (s *rpcServer) serveWS(ctx *fasthttp.RequestCtx) {
	err := upgrader.Upgrade(ctx, func(conn *websocket.Conn) {
		conn.SetReadLimit(maxRPCMessage)
		c := &wsClient{conn: conn}
		s.mu.Lock()
		s.clients[c] = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.clients, c)
			s.mu.Unlock()
		}()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil || c.reply(s, msg) != nil {
				return
			}
		}
	})
	if err != nil {
		reject(ctx, fasthttp.StatusBadRequest, "websocket: "+err.Error())
	}
}

// answer is the JSON-RPC answer to one request body.
func (s *rpcServer) answer(body []byte) []byte {
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return rpcReply(nil, nil, &rpcError{rpcParseError, "parse error: " + err.Error()})
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return rpcReply(req.ID, nil, &rpcError{rpcInvalidRequest, `send {"jsonrpc":"2.0","method":...}`})
	}
	result, rerr := s.call(req.Method, req.Params)
	return rpcReply(req.ID, result, rerr)
}

// notify sends a notification to each WebSocket client. A client that
// cannot take it ends on its next read.
func (s *rpcServer) notify(method, gid string) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": []any{map[string]string{"gid": gid}}}) //nolint:errchkjson // plain values
	s.mu.Lock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		c.send(b) //nolint:errcheck // see above
	}
}

func rpcReply(id json.RawMessage, result any, rerr *rpcError) []byte {
	if id == nil {
		id = json.RawMessage("null")
	}
	out := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		out["error"] = rerr
	} else {
		out["result"] = result
	}
	b, _ := json.Marshal(out) //nolint:errchkjson // the values are plain
	return b
}

func (s *rpcServer) call(method string, params json.RawMessage) (any, *rpcError) {
	var p struct {
		URLs []string `json:"urls"`
		Args []string `json:"args"` // native options, such as ["-o", "dl/", "-n", "4"]
		GID  string   `json:"gid"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{rpcInvalidParams, err.Error()}
		}
	}
	switch method {
	case "ghola.add":
		return s.add(p.URLs, p.Args)
	case "ghola.list":
		return s.list(), nil
	case "ghola.pause", "ghola.stop":
		return s.halt(p.GID, method == "ghola.stop")
	case "ghola.resume":
		return s.resume(p.GID)
	case "ghola.purge":
		return s.purge(), nil
	}
	return nil, &rpcError{rpcNoMethod, "no method " + method + "; use ghola.add, ghola.list, ghola.pause, ghola.resume, ghola.stop or ghola.purge"}
}

// add starts one job for each URL, with the options of ghola download.
func (s *rpcServer) add(urls, args []string) (any, *rpcError) {
	if len(urls) == 0 {
		return nil, &rpcError{rpcInvalidParams, "urls is required"}
	}
	for _, u := range urls {
		if !bridgeScheme(u) {
			return nil, &rpcError{rpcInvalidParams, "a url must be http:// or https://: " + u}
		}
	}
	c, err := cli.Parse(append([]string{cli.JobDownload}, args...))
	if err != nil {
		return nil, &rpcError{rpcInvalidParams, err.Error()}
	}
	if len(c.Args) > 0 {
		return nil, &rpcError{rpcInvalidParams, "args holds options only; the URLs go in urls"}
	}
	for name := range c.Values {
		if !rpcOptions[name] {
			return nil, &rpcError{rpcInvalidParams, "--" + name + " is not allowed over RPC"}
		}
	}
	var gids []string
	for _, u := range urls {
		j := &rpcJob{GID: newGID(), URL: u, args: args}
		if err := s.start(j, false); err != nil {
			return nil, &rpcError{rpcInvalidParams, err.Error()}
		}
		gids = append(gids, j.GID)
	}
	return gids, nil
}

// start runs the job in the background; resume adds -c.
func (s *rpcServer) start(j *rpcJob, resume bool) error {
	args := append([]string{cli.JobDownload}, j.args...)
	if resume {
		args = append(args, "-c")
		if j.File != "" {
			args = append(args, "-o", j.File)
		}
	}
	c, err := cli.Parse(append(args, j.URL))
	if err != nil {
		return err
	}
	opts, err := cli.Apply(c)
	if err != nil {
		return err
	}
	opts.Output.Silent, opts.Output.Progress = true, "off"
	opts.Output.Info = &config.TransferInfo{URL: j.URL}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	j.Status, j.Error, j.cancel, j.info, j.File = "active", "", cancel, opts.Output.Info, opts.Output.File
	j.run++
	run := j.run
	s.jobs[j.GID] = j
	s.mu.Unlock()
	go func() {
		err := client.Download(ctx, opts, s.stream)
		s.mu.Lock()
		event := ""
		if j.run == run {
			j.File = opts.Output.File
		}
		switch {
		case j.run != run: // a newer run owns the job
		case j.Status != "active": // paused or removed while it ran
		case err != nil:
			j.Status, j.Error, event = "error", err.Error(), rpcOnError
		default:
			j.Status, event = "complete", rpcOnComplete
		}
		s.mu.Unlock()
		if event != "" {
			s.notify(event, j.GID)
		}
	}()
	return nil
}

func (s *rpcServer) list() []rpcJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rpcJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		cp := *j
		if j.info != nil {
			cp.Bytes = j.info.LoadSize()
		}
		out = append(out, cp)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].GID < out[b].GID })
	return out
}

// halt pauses a job (resume restarts it with -c) or stops it (removed).
// The partial file stays in both cases, as with aria2.
func (s *rpcServer) halt(gid string, stop bool) (any, *rpcError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[gid]
	if !ok {
		return nil, &rpcError{rpcInvalidParams, "no job " + gid}
	}
	if j.Status == "active" {
		j.cancel()
	}
	j.Status = "paused"
	if stop {
		j.Status = "removed"
	}
	return gid, nil
}

func (s *rpcServer) resume(gid string) (any, *rpcError) {
	s.mu.Lock()
	j, ok := s.jobs[gid]
	s.mu.Unlock()
	if !ok || j.Status != "paused" {
		return nil, &rpcError{rpcInvalidParams, "no paused job " + gid}
	}
	if err := s.start(j, true); err != nil {
		return nil, &rpcError{rpcInvalidParams, err.Error()}
	}
	return gid, nil
}

// purge drops the jobs that are done: complete, error or removed.
func (s *rpcServer) purge() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for gid, j := range s.jobs {
		if j.Status != "active" && j.Status != "paused" {
			delete(s.jobs, gid)
			n++
		}
	}
	return n
}

// newGID is a 16-digit hex job ID, as aria2's GID.
func newGID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck // crypto/rand does not fail
	return hex.EncodeToString(b)
}
