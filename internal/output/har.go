package output

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// HAR represents an HTTP Archive 1.2 document.
type HAR struct {
	Log HARLog `json:"log"`
}

// HARLog is the top-level log object.
type HARLog struct {
	Version string     `json:"version"`
	Creator HARCreator `json:"creator"`
	Entries []HAREntry `json:"entries"`
}

// HARCreator identifies the tool that generated the archive.
type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// HAREntry captures a single request/response exchange.
type HAREntry struct {
	StartedDateTime string      `json:"startedDateTime"`
	Time            float64     `json:"time"`
	Request         HARRequest  `json:"request"`
	Response        HARResponse `json:"response"`
	Cache           struct{}    `json:"cache"`
	Timings         HARTimings  `json:"timings"`
}

// HARRequest describes the outgoing request.
type HARRequest struct {
	Method      string   `json:"method"`
	URL         string   `json:"url"`
	HTTPVersion string   `json:"httpVersion"`
	Headers     []HARKV  `json:"headers"`
	QueryString []HARKV  `json:"queryString"`
	Cookies     []HARKV  `json:"cookies"`
	HeadersSize int      `json:"headersSize"`
	BodySize    int      `json:"bodySize"`
	PostData    *HARPost `json:"postData,omitempty"`
}

// HARResponse describes the server response.
type HARResponse struct {
	Status      int        `json:"status"`
	StatusText  string     `json:"statusText"`
	HTTPVersion string     `json:"httpVersion"`
	Headers     []HARKV    `json:"headers"`
	Cookies     []HARKV    `json:"cookies"`
	Content     HARContent `json:"content"`
	RedirectURL string     `json:"redirectURL"`
	HeadersSize int        `json:"headersSize"`
	BodySize    int        `json:"bodySize"`
}

// HARContent holds the response body metadata.
type HARContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
}

// HARTimings holds timing breakdown (use -1 for unknown).
type HARTimings struct {
	Blocked float64 `json:"blocked"`
	DNS     float64 `json:"dns"`
	Connect float64 `json:"connect"`
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
	SSL     float64 `json:"ssl"`
}

// HARKV is a generic name/value pair.
type HARKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HARPost holds request body data.
type HARPost struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

// WriteHAR exports the request/response pair as a HAR 1.2 JSON file.
func WriteHAR(path string, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response, start time.Time, elapsed time.Duration) error {
	reqHeaders := collectHeaders(func(visit func(key, value []byte)) { req.Header.VisitAll(visit) })
	rspHeaders := collectHeaders(func(visit func(key, value []byte)) { rsp.Header.VisitAll(visit) })

	queryString := parseQueryString(string(req.URI().QueryString()))

	entry := HAREntry{
		StartedDateTime: start.UTC().Format(time.RFC3339Nano),
		Time:            float64(elapsed.Milliseconds()),
		Request: HARRequest{
			Method:      string(req.Header.Method()),
			URL:         req.URI().String(),
			HTTPVersion: "HTTP/1.1",
			Headers:     reqHeaders,
			QueryString: queryString,
			Cookies:     []HARKV{},
			HeadersSize: len(req.Header.Header()),
			BodySize:    len(req.Body()),
		},
		Response: HARResponse{
			Status:      rsp.StatusCode(),
			StatusText:  fasthttp.StatusMessage(rsp.StatusCode()),
			HTTPVersion: "HTTP/1.1",
			Headers:     rspHeaders,
			Cookies:     []HARKV{},
			Content: HARContent{
				Size:     len(rsp.Body()),
				MimeType: string(rsp.Header.ContentType()),
				Text:     string(rsp.Body()),
			},
			RedirectURL: string(rsp.Header.Peek("Location")),
			HeadersSize: len(rsp.Header.Header()),
			BodySize:    len(rsp.Body()),
		},
		Timings: HARTimings{
			Blocked: -1,
			DNS:     -1,
			Connect: -1,
			Send:    -1,
			Wait:    float64(elapsed.Milliseconds()),
			Receive: -1,
			SSL:     -1,
		},
	}

	if len(req.Body()) > 0 {
		ct := string(req.Header.ContentType())
		if ct == "" {
			ct = "application/octet-stream"
		}
		entry.Request.PostData = &HARPost{
			MimeType: ct,
			Text:     string(req.Body()),
		}
	}

	har := HAR{
		Log: HARLog{
			Version: "1.2",
			Creator: HARCreator{Name: "ghola", Version: config.Version},
			Entries: []HAREntry{entry},
		},
	}

	data, err := json.MarshalIndent(har, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal HAR: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

func collectHeaders(visit func(func(key, value []byte))) []HARKV {
	var headers []HARKV
	visit(func(key, value []byte) {
		headers = append(headers, HARKV{Name: string(key), Value: string(value)})
	})
	if headers == nil {
		headers = []HARKV{}
	}
	return headers
}

func parseQueryString(qs string) []HARKV {
	if qs == "" {
		return []HARKV{}
	}
	values, err := url.ParseQuery(qs)
	if err != nil {
		return []HARKV{}
	}
	var result []HARKV
	for key, vals := range values {
		for _, val := range vals {
			result = append(result, HARKV{Name: key, Value: val})
		}
	}
	if result == nil {
		result = []HARKV{}
	}
	return result
}
