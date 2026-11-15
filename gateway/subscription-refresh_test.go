package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionRefreshPersistsAndRunsDueSchedule(t *testing.T) {
	var pulls atomic.Int32
	var failed atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/subs":
			w.Write([]byte(`{"status":"success","data":[{"name":"test","source":"remote","url":"https://test.invalid/sub?secret=fixture"}]}`))
		case "/download/test/JSON":
			pulls.Add(1)
			if failed.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Header().Set("subscription-userinfo", "upload=0; download=0; total=1024; expire=4102444800")
			w.Write([]byte(`[{"name":"fixture"}]`))
		default:
			t.Errorf("unexpected backend request %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	statePath := filepath.Join(t.TempDir(), "details.json")
	open := func() *subscriptionRefresh {
		t.Helper()
		s, err := openSubscriptionRefresh(upstream.URL, statePath)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	request := func(method, suffix, body string, trusted bool, expected int) []byte {
		t.Helper()
		path := subscriptionDetailsPath + suffix
		out := httptest.NewRecorder()
		s.serve(out, httptest.NewRequest(method, path, strings.NewReader(body)), path, trusted)
		if out.Code != expected {
			t.Fatalf("%s %s returned %d: %s", method, path, out.Code, out.Body.String())
		}
		return out.Body.Bytes()
	}
	read := func() subscriptionDetails {
		t.Helper()
		var details map[string]subscriptionDetails
		if err := json.Unmarshal(request("GET", "", "", true, http.StatusOK), &details); err != nil {
			t.Fatal(err)
		}
		return details["test"]
	}
	assertPulls := func(want int) {
		t.Helper()
		if got := int(pulls.Load()); got != want {
			t.Fatalf("got %d upstream pulls, want %d", got, want)
		}
	}
	if read().Snapshot != nil {
		t.Fatal("unexpected snapshot before first refresh")
	}
	assertPulls(0)
	request("PUT", "/schedule", `{"name":"test","intervalMinutes":1}`, false, http.StatusForbidden)
	request("PUT", "/schedule", `{"name":"test","intervalMinutes":43201}`, true, http.StatusBadRequest)
	request("PUT", "/schedule", `{"name":"test","intervalMinutes":1}`, true, http.StatusOK)
	request("POST", "/refresh", `{"name":"test"}`, true, http.StatusOK)
	assertPulls(1)

	s = open()
	details := read()
	if details.Snapshot == nil || details.Snapshot.Count != 1 || details.Snapshot.Used == nil || *details.Snapshot.Used != 0 ||
		details.Snapshot.Total != 1024 || details.IntervalMinutes != 1 || details.NextUpdateAt <= time.Now().UnixMilli() {
		t.Fatalf("incomplete persisted state: %+v", details)
	}
	s.due(t.Context())
	assertPulls(1)
	makeDue := func() {
		t.Helper()
		record := s.records["test"]
		record.Details.NextUpdateAt = time.Now().Add(-time.Minute).UnixMilli()
		s.records["test"] = record
	}
	makeDue()
	failed.Store(true)
	s.due(t.Context())
	assertPulls(2)
	retained := read()
	if retained.LastError == "" || retained.Snapshot == nil || retained.Snapshot.UpdatedAt != details.Snapshot.UpdatedAt ||
		retained.NextUpdateAt <= time.Now().UnixMilli() {
		t.Fatalf("snapshot or schedule lost after failed update: %+v", retained)
	}
	makeDue()
	failed.Store(false)
	s.due(t.Context())
	assertPulls(3)
	if read().LastError != "" {
		t.Fatal("successful scheduled update did not clear the error")
	}
	request("PUT", "/schedule", `{"name":"test","intervalMinutes":0}`, true, http.StatusOK)
	s = open()
	s.due(t.Context())
	assertPulls(3)
	if details = read(); details.NextUpdateAt != 0 || details.IntervalMinutes != 0 || details.Snapshot == nil {
		t.Fatalf("unexpected disabled state: %+v", details)
	}
}
