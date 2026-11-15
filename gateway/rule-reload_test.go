package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func checkRuleReloadAndRollback(t *testing.T, core *exec.Cmd, selection *subscriptionSelection, path, original string) {
	t.Helper()
	const existing = "rules: ['MATCH,Sub-Store 订阅']"
	candidate := strings.Replace(original, existing, "rules: ['DOMAIN-SUFFIX,example.test,DIRECT', 'MATCH,Sub-Store 订阅']", 1)
	if candidate == original {
		t.Fatal("missing test rule list")
	}
	candidatePath := path + ".candidate"
	if err := os.WriteFile(candidatePath, []byte(candidate), 0600); err != nil {
		t.Fatal(err)
	}
	check := exec.Command(core.Path, "-t", "-d", core.Args[2], "-f", candidatePath)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("Mihomo rejected the candidate: %v: %s", err, out)
	}
	request := func(method, path string, body []byte) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, selection.routerURL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := selection.controller.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, raw
	}
	rules := func() []string {
		t.Helper()
		code, raw := request(http.MethodGet, "/rules", nil)
		var payload struct {
			Rules []struct{ Payload, Proxy string }
		}
		if code != http.StatusOK || json.Unmarshal(raw, &payload) != nil {
			t.Fatalf("unable to read rules: HTTP %d", code)
		}
		result := make([]string, 0, len(payload.Rules))
		for _, entry := range payload.Rules {
			result = append(result, entry.Payload+" → "+entry.Proxy)
		}
		return result
	}
	apply := func(config string) int {
		t.Helper()
		body, err := json.Marshal(map[string]string{"payload": config})
		if err != nil {
			t.Fatal(err)
		}
		code, _ := request(http.MethodPut, "/configs?force=false", body)
		return code
	}
	baseline := rules()
	if code := apply("rules: ["); code != http.StatusBadRequest || !reflect.DeepEqual(baseline, rules()) {
		t.Fatalf("invalid config must leave the rules active, got HTTP %d", code)
	}
	if code := apply(candidate); code != http.StatusNoContent {
		t.Fatalf("candidate apply returned HTTP %d", code)
	}
	want := append([]string{"example.test → DIRECT"}, baseline...)
	if got := rules(); !reflect.DeepEqual(got, want) {
		t.Fatalf("active rules after reload: got %v, want %v", got, want)
	}
	if code := apply(original); code != http.StatusNoContent || !reflect.DeepEqual(baseline, rules()) {
		t.Fatalf("rollback failed, got HTTP %d", code)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != original {
		t.Fatalf("native controller unexpectedly wrote the startup config: %v", err)
	}
	group, err := selection.group(t.Context())
	if err != nil || group.Now != "Source-B" {
		t.Fatalf("subscription selection changed during reload and rollback: %v, %v", group.Now, err)
	}
	t.Logf("verified validate, reload, and rollback against the same Mihomo process")
}
