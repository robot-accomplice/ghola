package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// SigV4 is the --auth-aws-sigv4 value: provider1[:provider2[:region[:service]]],
// as curl --aws-sigv4 takes it.
type SigV4 struct {
	Provider1, Provider2, Region, Service string
}

// ParseSigV4 reads the value, and checks that the region and the service
// are known for rawURL, the first URL.
func ParseSigV4(value, rawURL string) (*SigV4, error) {
	parts := strings.Split(value, ":")
	if value == "" || len(parts) > 4 || parts[0] == "" {
		return nil, fmt.Errorf("--auth-aws-sigv4 %q: use provider1[:provider2[:region[:service]]], such as aws:amz:us-east-1:s3", value)
	}
	for len(parts) < 4 {
		parts = append(parts, "")
	}
	s := &SigV4{Provider1: strings.ToLower(parts[0]), Provider2: strings.ToLower(parts[1]), Region: parts[2], Service: parts[3]}
	if s.Provider2 == "" {
		s.Provider2 = s.Provider1
	}
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		if _, _, err := s.RegionService(u.Hostname()); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// RegionService returns the region and the service: from the value, else
// from a host such as s3.eu-west-1.amazonaws.com.
func (s SigV4) RegionService(host string) (string, string, error) {
	region, service := s.Region, s.Service
	if region != "" && service != "" {
		return region, service, nil
	}
	labels := strings.Split(host, ".")
	if len(labels) < 3 || net.ParseIP(host) != nil {
		return "", "", fmt.Errorf("--auth-aws-sigv4: give the region and the service; the host %s does not name them", host)
	}
	if service == "" {
		service = labels[0]
	}
	if region == "" {
		region = labels[1]
	}
	return region, service, nil
}
