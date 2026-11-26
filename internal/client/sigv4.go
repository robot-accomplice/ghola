package client

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// signSigV4 sets the date, the payload hash (for S3, which requires it)
// and the Authorization header of req, signed with -u access:secret
// (--auth-aws-sigv4, as curl --aws-sigv4).
func signSigV4(s config.SigV4, req *fasthttp.Request, user string, now time.Time) error {
	access, secret, _ := strings.Cut(user, ":")
	u, err := url.Parse(string(req.URI().FullURI()))
	if err != nil {
		return err
	}
	region, service, err := s.RegionService(u.Hostname())
	if err != nil {
		return err
	}
	p1 := strings.ToUpper(s.Provider1)
	dateHeader := "x-" + s.Provider2 + "-date"
	amzDate, day := now.UTC().Format("20060102T150405Z"), now.UTC().Format("20060102")
	payload := sha256Hex(req.Body())
	req.Header.Set(dateHeader, amzDate)
	headers := map[string]string{"host": u.Host, dateHeader: amzDate}
	if service == "s3" {
		h := "x-" + s.Provider2 + "-content-sha256"
		req.Header.Set(h, payload)
		headers[h] = payload
	}
	names := make([]string, 0, len(headers))
	for n := range headers {
		names = append(names, n)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		canonHeaders.WriteString(n + ":" + strings.TrimSpace(headers[n]) + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{string(req.Header.Method()), canonicalPath(u.EscapedPath(), service), canonicalQuery(u.Query()), canonHeaders.String(), signed, payload}, "\n")
	scope := day + "/" + region + "/" + service + "/" + s.Provider1 + "4_request"
	algo := p1 + "4-HMAC-SHA256"
	toSign := algo + "\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := []byte(p1 + "4" + secret)
	for _, part := range []string{day, region, service, s.Provider1 + "4_request"} {
		key = hmacSHA256(key, part)
	}
	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algo, access, scope, signed, hex.EncodeToString(hmacSHA256(key, toSign))))
	return nil
}

// canonicalPath returns the path of the canonical request. AWS encodes
// each segment of the path as sent once more, except for S3, which signs
// the path as sent. So an encoded slash (%2F) stays part of its segment.
func canonicalPath(escaped, service string) string {
	if escaped == "" {
		return "/"
	}
	if service == "s3" {
		return escaped
	}
	segments := strings.Split(escaped, "/")
	for i, seg := range segments {
		segments[i] = awsEscape(seg)
	}
	return strings.Join(segments, "/")
}

// canonicalQuery sorts the parameters and encodes them as RFC 3986 says.
func canonicalQuery(q url.Values) string {
	var pairs []string
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, awsEscape(k)+"="+awsEscape(v))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func awsEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data)) //nolint:errcheck // a hash does not fail
	return m.Sum(nil)
}
