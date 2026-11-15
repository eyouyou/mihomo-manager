package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type controllerTransport struct {
	upstream http.RoundTripper
	host     string
	token    string
}

func (t controllerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != t.host {
		return nil, errors.New("invalid Mihomo controller destination")
	}
	r = r.Clone(r.Context())
	r.Host = t.host
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errors.New("invalid Mihomo controller query")
	}
	query.Del("token")
	r.URL.RawQuery = query.Encode()
	r.Header.Del("Cookie")
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.upstream.RoundTrip(r)
}

func routerControllerTransport(dir string, target *url.URL) (http.RoundTripper, error) {
	ca, err := os.ReadFile(filepath.Join(dir, "mihomo-controller-ca.crt"))
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid Mihomo controller CA")
	}
	data, err := os.ReadFile(filepath.Join(dir, "mihomo-controller-token"))
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return nil, errors.New("invalid Mihomo controller token")
	}
	upstream := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
	}
	return controllerTransport{upstream: upstream, host: target.Host, token: token}, nil
}
