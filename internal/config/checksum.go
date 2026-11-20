package config

import (
	"bytes"
	"crypto/md5"  //nolint:gosec // aria2 --checksum takes md5; the user picks it
	"crypto/sha1" //nolint:gosec // aria2 --checksum takes sha-1; the user picks it
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

// ErrChecksum marks a body whose digest differs from --checksum or from
// the server's Content-Digest. It exits 32, aria2's code for a failed
// checksum; curl does not use 32.
var ErrChecksum = errors.New("checksum mismatch")

// digestAlgos are the --checksum algorithms, by aria2's names, which are
// also the names of RFC 9530 (Content-Digest).
var digestAlgos = map[string]func() hash.Hash{
	"md5": md5.New, "sha-1": sha1.New, "sha-224": sha256.New224, "sha-256": sha256.New,
	"sha-384": sha512.New384, "sha-512": sha512.New,
}

// Digest is an expected digest: the algorithm and the bytes.
type Digest struct {
	Algo string
	Want []byte
}

// ParseChecksum reads --checksum algo=hex, as aria2 takes it.
func ParseChecksum(v string) (*Digest, error) {
	algo, hexSum, ok := strings.Cut(v, "=")
	algo = strings.ToLower(algo)
	newHash, known := digestAlgos[algo]
	want, err := hex.DecodeString(hexSum)
	if !ok || !known || err != nil || len(want) != newHash().Size() {
		return nil, fmt.Errorf("--checksum %q: use ALGO=HEX, with ALGO one of md5, sha-1, sha-224, sha-256, sha-384, sha-512", v)
	}
	return &Digest{Algo: algo, Want: want}, nil
}

// ContentDigest reads the first known digest of a Content-Digest header
// (RFC 9530: sha-256=:BASE64:, ...). It returns nil when there is none.
func ContentDigest(header string) *Digest {
	for _, item := range strings.Split(header, ",") {
		algo, val, ok := strings.Cut(strings.TrimSpace(item), "=")
		algo = strings.ToLower(algo)
		if _, known := digestAlgos[algo]; !ok || !known {
			continue
		}
		want, err := base64.StdEncoding.DecodeString(strings.Trim(strings.TrimSpace(val), ":"))
		if err == nil {
			return &Digest{Algo: algo, Want: want}
		}
	}
	return nil
}

// DigestFor returns the digest that a whole answer must match:
// --checksum, else the server's Content-Digest. Content-Digest covers the
// bytes as they were sent, so it counts only for a whole 200 answer that
// ghola does not decode.
func (o OutputOptions) DigestFor(status int, contentDigest, contentEncoding string) *Digest {
	if o.Checksum != nil {
		return o.Checksum
	}
	if status != 200 || (contentEncoding != "" && !strings.EqualFold(contentEncoding, "identity")) {
		return nil
	}
	return ContentDigest(contentDigest)
}

// NewHash returns a hash for the digest's algorithm.
func (d *Digest) NewHash() hash.Hash { return digestAlgos[d.Algo]() }

// Check compares a computed sum with the digest.
func (d *Digest) Check(sum []byte, what string) error {
	if !bytes.Equal(sum, d.Want) {
		return fmt.Errorf("%w: %s %s is %x, want %x", ErrChecksum, what, d.Algo, sum, d.Want)
	}
	return nil
}

// CheckFile hashes the file at path and compares it.
func (d *Digest) CheckFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := d.NewHash()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	return d.Check(h.Sum(nil), path)
}
