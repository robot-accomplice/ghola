package mirror

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
)

// item is one URL in the queue.
type item struct {
	url       *url.URL
	depth     int
	requisite bool
}

// Mirror copies the site of base.URL into root.
type Mirror struct {
	Base   *config.Options // the options of each request
	Opts   config.MirrorOptions
	Root   string
	Do     client.Doer
	Stream client.Streamer
	Log    io.Writer // one line for each saved file and each failure; nil is quiet
	// ExitCode maps a failure to its exit code, for the error events of
	// --output-events.
	ExitCode func(error) int

	scope  *scope
	queue  []item
	seen   map[string]bool
	saved  map[string]string // URL -> local file, for --convert-links
	robots map[string]robotsRules
	total  int64
	last   error
	sent   int
}

// Run follows the links from the start URL. It goes on after a failed
// URL, as wget does, and returns the last failure, as ghola does for
// several URLs (v1 defaults, item 10B).
func (m *Mirror) Run(ctx context.Context) error {
	start, err := url.Parse(m.Base.URL)
	if err != nil {
		return err
	}
	m.scope = newScope(start, m.Opts)
	m.seen, m.saved, m.robots = map[string]bool{}, map[string]string{}, map[string]robotsRules{}
	m.add(start, 0, false)
	for len(m.queue) > 0 && ctx.Err() == nil {
		it := m.queue[0]
		m.queue = m.queue[1:]
		if m.Opts.Quota > 0 && m.total >= m.Opts.Quota {
			m.logf("Stopped: --quota %d bytes is reached", m.Opts.Quota)
			break
		}
		if m.Opts.Robots && !m.robotsAllow(ctx, it.url) {
			continue
		}
		m.visit(ctx, it)
	}
	if m.Opts.Convert && !m.Opts.Spider {
		if err := m.convertAll(); err != nil && m.last == nil {
			m.last = err
		}
	}
	if err := ctx.Err(); err != nil && m.last == nil {
		m.last = err
	}
	return m.last
}

// add puts u in the queue once, when the scope allows it.
func (m *Mirror) add(u *url.URL, depth int, requisite bool) {
	key := u.String()
	if m.seen[key] || !m.scope.allows(u, requisite) {
		return
	}
	m.seen[key] = true
	m.queue = append(m.queue, item{url: u, depth: depth, requisite: requisite})
}

// visit fetches one URL, saves it, and queues its links.
func (m *Mirror) visit(ctx context.Context, it item) {
	keep := m.scope.keeps(it.url)
	if !keep && !looksLikePage(it.url) {
		return // a rejected name that cannot hold links is not fetched, as in wget
	}
	m.pause(ctx)
	if m.Opts.Spider {
		m.spider(ctx, it)
		return
	}
	local := localPath(m.Root, it.url, m.Opts.Layout, m.Opts.CutDirs, m.Opts.FileNames)
	opts := m.requestOptions(it.url, local)
	opts.Output.Events.Start(opts.URL)
	began := time.Now()
	err := client.Download(ctx, opts, m.Stream)
	info := opts.Output.Info
	info.Time = time.Since(began)
	if err != nil {
		info.ExitCode, info.ErrorMsg = m.exitCode(err), err.Error()
		opts.Output.Events.End(*info)
		m.fail(it.url, err)
		return
	}
	info.File = local
	opts.Output.Events.End(*info)
	m.total += info.Size
	if m.Opts.AdjustExt {
		if adjusted := withExtension(local, info.ContentType); adjusted != local {
			if err := os.Rename(local, adjusted); err == nil {
				local = adjusted
			}
		}
	}
	kind := pageKind(info.ContentType, local)
	if kind != "" {
		if body, err := os.ReadFile(local); err == nil {
			m.follow(it, kind, body)
		}
	}
	if !keep {
		os.Remove(local) //nolint:errcheck // read for its links only, as wget -R does
		return
	}
	m.saved[it.url.String()] = local
	if info.Status != http.StatusNotModified {
		m.logf("Saved %s (%d bytes)", local, info.Size)
	}
}

// requestOptions are the options of one download: the base options, the
// local file, and --timestamps.
func (m *Mirror) requestOptions(u *url.URL, local string) *config.Options {
	o := *m.Base
	o.URL, o.URLs = u.String(), []string{u.String()}
	o.Output.File, o.Output.Stdout, o.Output.RemoteName, o.Output.Dir = local, false, false, ""
	o.Output.CreateDirs, o.Output.NoOverwrite = true, false
	o.Output.Info = &config.TransferInfo{URL: o.URL}
	o.Output.NoSession = true // the mirror runs again as a whole, not file by file
	o.Headers = append([]string(nil), m.Base.Headers...)
	if m.Opts.Timestamps {
		o.Output.RemoteTime = true
		if fi, err := os.Stat(local); err == nil {
			o.Headers = append(o.Headers, "If-Modified-Since: "+fi.ModTime().UTC().Format(http.TimeFormat))
			o.Output.CondTime = true // a 304 keeps the file
		}
	}
	return &o
}

// follow queues the links of a page. A page at the depth limit gives only
// its requisites (with --requisites), as wget -p does.
func (m *Mirror) follow(it item, kind string, body []byte) {
	var links []link
	nofollow := false
	if kind == "css" {
		links = cssLinks(it.url, body)
	} else {
		links, nofollow = pageLinks(it.url, body)
	}
	deeper := m.Opts.Depth == 0 || it.depth < m.Opts.Depth
	for _, l := range links {
		switch {
		case l.requisite && (m.Opts.Requisites || kind == "css"):
			m.add(l.url, it.depth+1, true)
		case deeper && !it.requisite && (!m.Opts.Robots || (!nofollow && !l.nofollow)):
			m.add(l.url, it.depth+1, false)
		}
	}
}

// spider fetches a URL without saving it, as wget --spider does, and
// reports a broken link. It reads the links of a page.
func (m *Mirror) spider(ctx context.Context, it item) {
	opts := *m.Base
	opts.URL, opts.URLs = it.url.String(), []string{it.url.String()}
	opts.Output.Fail = false
	req, rsp, err := client.FetchURL(ctx, &opts, m.Do)
	if err != nil {
		m.fail(it.url, err)
		return
	}
	defer client.Release(req, rsp)
	status := rsp.StatusCode()
	if status >= http.StatusBadRequest {
		m.fail(it.url, fmt.Errorf("%w: %d", config.ErrHTTPStatus, status))
		return
	}
	m.logf("OK %d %s", status, it.url)
	if kind := pageKind(string(rsp.Header.ContentType()), it.url.Path); kind != "" {
		if body, err := gholatransport.ReadBody(rsp); err == nil {
			m.follow(it, kind, body)
		}
	}
}

// robotsAllow reads robots.txt once for each host, and applies it.
func (m *Mirror) robotsAllow(ctx context.Context, u *url.URL) bool {
	key := u.Scheme + "://" + u.Host
	rules, ok := m.robots[key]
	if !ok {
		opts := *m.Base
		opts.URL = key + "/robots.txt"
		opts.URLs = []string{opts.URL}
		opts.Output.Fail = false
		if req, rsp, err := client.FetchURL(ctx, &opts, m.Do); err == nil {
			if rsp.StatusCode() == http.StatusOK {
				if body, err := gholatransport.ReadBody(rsp); err == nil {
					rules = parseRobots(strings.NewReader(string(body)))
				}
			}
			client.Release(req, rsp)
		}
		m.robots[key] = rules
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return rules.allows(p)
}

// pause waits --wait between requests.
func (m *Mirror) pause(ctx context.Context) {
	if m.sent > 0 && m.Base.Resilience.Wait > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(m.Base.Resilience.Wait):
		}
	}
	m.sent++
}

// exitCode is the exit code of a failure; 55 (another cause) without
// ExitCode.
func (m *Mirror) exitCode(err error) int {
	if m.ExitCode == nil {
		return config.SendFailed.Int()
	}
	return m.ExitCode(err)
}

func (m *Mirror) fail(u *url.URL, err error) {
	m.last = err
	m.logf("Failed: %s: %v", u, err)
}

func (m *Mirror) logf(format string, args ...any) {
	if m.Log != nil {
		fmt.Fprintf(m.Log, format+"\n", args...)
	}
}

// pageKind is "html" or "css" when the file can hold links, else "".
func pageKind(contentType, name string) string {
	ct := strings.ToLower(contentType)
	ext := strings.ToLower(path.Ext(strings.TrimSuffix(filepath.ToSlash(name), "/")))
	switch {
	case strings.Contains(ct, "html"), ct == "" && (ext == ".html" || ext == ".htm"):
		return "html"
	case strings.Contains(ct, "text/css"), ct == "" && ext == ".css":
		return "css"
	}
	return ""
}

// looksLikePage reports a name that may hold an HTML page: a folder, no
// extension, or a page extension. wget fetches such a rejected name to
// read its links.
func looksLikePage(u *url.URL) bool {
	ext := strings.ToLower(path.Ext(u.Path))
	return ext == "" || strings.HasSuffix(u.Path, "/") || strings.Contains(".html.htm.php.asp.aspx.jsp.shtml.cgi", ext)
}
