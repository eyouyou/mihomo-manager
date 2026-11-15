package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTrafficRulesBrowser(t *testing.T) {
	browser := os.Getenv("SUBSCRIPTION_BROWSER_TEST")
	if browser == "" {
		t.Skip("SUBSCRIPTION_BROWSER_TEST is not set")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rules := make([]map[string]any, 76)
		for i := range rules {
			rules[i] = map[string]any{"index": i, "type": "DOMAIN", "payload": fmt.Sprintf("rule-%d.example.test", i),
				"proxy": "DIRECT", "size": -1, "extra": map[string]any{"disabled": i == 1}}
		}
		rules[75]["proxy"] = "Fixture-Download"
		json.NewEncoder(w).Encode(map[string]any{"rules": rules})
	}))
	defer upstream.Close()
	directory := "../subscriptions/dist"
	rules := trafficRules{client: upstream.Client(), routerURL: upstream.URL, directory: directory}
	assets := http.StripPrefix(managementPath, http.FileServer(http.Dir(filepath.Clean(directory))))
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == trafficRulesPath+"/api/rules" {
			rules.ServeHTTP(w, r)
		} else {
			serveManagement(w, r, directory, assets)
		}
	}))
	defer app.Close()
	cmd := exec.Command("node", browser, "test", "traffic-rules.spec.ts", "--workers=1")
	cmd.Dir = "../subscriptions"
	cmd.Env = append(os.Environ(), "TRAFFIC_RULES_TEST_URL="+app.URL)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rules browser check failed: %v\n%s", err, output)
	}
}
