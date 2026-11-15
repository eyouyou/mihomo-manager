package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestTrafficRulesReadOnly(t *testing.T) {
	const snapshot = `{"rules":[{"index":0,"type":"DOMAIN","payload":"example.test","proxy":"DIRECT","size":-1,"extra":{"disabled":true,"hitCount":12}},{"index":1,"type":"MATCH","payload":"","proxy":"Fallback","size":-1}]}`
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/rules" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected router request: %s %s", r.Method, r.URL.RequestURI())
		}
		w.Write([]byte(snapshot))
	}))
	defer upstream.Close()
	handler := trafficRules{routerURL: upstream.URL, client: upstream.Client()}
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{http.MethodPatch, "/api/rules", 405},
		{http.MethodPut, "/api/rules", 405},
		{http.MethodPost, "/api/rules", 405},
		{http.MethodDelete, "/api/rules", 405},
		{http.MethodGet, "/api/rules/disable", 404},
		{http.MethodGet, "/api/rules?ignored=1", 200},
	} {
		req := httptest.NewRequest(tc.method, trafficRulesPath+tc.path, nil)
		req.Header.Set("Cookie", "session=test")
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if out.Code != tc.code {
			t.Fatalf("%s %s: got HTTP %d, want %d", tc.method, tc.path, out.Code, tc.code)
		}
		if tc.code == 200 && out.Body.String() != snapshot {
			t.Fatal("rules snapshot lost order or fields")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("got %d router calls, want 1", calls.Load())
	}
}
