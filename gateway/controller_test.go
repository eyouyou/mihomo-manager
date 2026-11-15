package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestControllerTransport(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-controller-token" || r.Header.Get("Cookie") != "" ||
			r.URL.Query().Has("token") || r.URL.Query().Get("level") != "info" {
			t.Errorf("incorrect controller request")
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	for name, data := range map[string][]byte{"mihomo-controller-token": []byte("test-controller-token"), "mihomo-controller-ca.crt": ca} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	target, _ := url.Parse(upstream.URL)
	tr, err := routerControllerTransport(dir, target)
	if err != nil {
		t.Fatal(err)
	}
	transport := tr
	request := httptest.NewRequest("GET", upstream.URL+"/logs?token=browser&level=info", nil)
	request.Header.Set("Authorization", "Bearer browser")
	request.Header.Set("Cookie", "browser=session")
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 || request.Header.Get("Authorization") != "Bearer browser" {
		t.Fatal("unexpected response or mutated caller request")
	}
	if _, err := transport.RoundTrip(httptest.NewRequest("GET", "https://example.invalid/rules", nil)); err == nil {
		t.Fatal("transport accepted a different destination")
	}
}
