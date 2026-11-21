package client

import (
	"strings"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// The signature matches the "get-vanilla" case of the AWS SigV4 test
// suite, which AWS computed with its own signer: a wrong canonical form,
// scope or key derivation would not give this value.
func TestSigV4_GetVanilla(t *testing.T) {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("https://example.amazonaws.com/")
	req.Header.SetMethod("GET")
	s, err := config.ParseSigV4("aws:amz:us-east-1:service", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	if err := signSigV4(*s, req, "AKIDEXAMPLE:wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", now); err != nil {
		t.Fatal(err)
	}
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := string(req.Header.Peek("Authorization")); got != want {
		t.Errorf("Authorization\n got %s\nwant %s", got, want)
	}
}

// S3 also signs the payload hash, which S3 requires; the region and the
// service come from the host when the value leaves them out.
func TestSigV4_S3FromHost(t *testing.T) {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("https://s3.eu-west-1.amazonaws.com/bucket/key")
	req.Header.SetMethod("GET")
	s, _ := config.ParseSigV4("aws:amz", "")
	if err := signSigV4(*s, req, "AK:SK", time.Now()); err != nil {
		t.Fatal(err)
	}
	got := string(req.Header.Peek("Authorization"))
	if !strings.Contains(got, "/eu-west-1/s3/aws4_request") || !strings.Contains(got, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Errorf("Authorization = %s", got)
	}
	if _, err := config.ParseSigV4("a:b:c:d:e", ""); err == nil {
		t.Error("five parts were accepted")
	}
	if _, err := config.ParseSigV4("aws:amz", "https://localhost/"); err == nil {
		t.Error("a host without the region and the service was accepted")
	}
}
