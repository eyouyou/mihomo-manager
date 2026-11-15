package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionRelease(t *testing.T) {
	mihomo, browser := os.Getenv("MIHOMO_BIN"), os.Getenv("SUBSCRIPTION_BROWSER_TEST")
	if mihomo == "" || browser == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("release check requires MIHOMO_BIN and SUBSCRIPTION_BROWSER_TEST")
		}
		t.Skip("set MIHOMO_BIN and SUBSCRIPTION_BROWSER_TEST to run the release check")
	}
	dir := t.TempDir()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	probe.Close()
	const token = "ci-native-https-token"
	key := filepath.Join(dir, "tls.key")
	cert := filepath.Join(dir, "mihomo-controller-ca.crt")
	gen := exec.Command("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1",
		"-nodes", "-days", "1", "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1",
		"-keyout", key, "-out", cert)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("creating a test certificate: %v: %s", err, out)
	}
	config := "external-controller-tls: " + address + "\nsecret: " + token + `
tls:
  certificate: ./mihomo-controller-ca.crt
  private-key: ./tls.key
profile:
  store-selected: true
proxy-providers:
  Fixture-A: {type: file, path: ./source-a.yaml}
  Fixture-B: {type: file, path: ./source-b.yaml}
proxy-groups:
  - {name: Legacy, type: select, proxies: [DIRECT], use: [Fixture-A, Fixture-B]}
  - {name: Source-A, type: select, use: [Fixture-A]}
  - {name: Source-B, type: select, use: [Fixture-B]}
  - {name: Sub-Store 订阅, type: select, proxies: [Source-A, Source-B]}
rules: ['MATCH,Sub-Store 订阅']
`
	write := func(name, data string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("source-a.yaml", "proxies: [{name: Fixture-Node-A, type: direct}]\n")
	write("source-b.yaml", "proxies: [{name: Fixture-Node-B, type: direct}]\n")
	path := write("config.yaml", config)
	write("mihomo-controller-token", token)
	target, _ := url.Parse("https://" + address)
	transport, err := routerControllerTransport(dir, target)
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "mihomo.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	core := exec.Command(mihomo, "-d", dir, "-f", path)
	core.Stdout, core.Stderr = log, log
	if err := core.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { core.Process.Kill(); core.Wait() })
	selection := &subscriptionSelection{
		controller: &http.Client{Transport: transport, Timeout: time.Second},
		routerURL:  target.String(),
		bindings:   write("bindings.json", `{"first":"Source-A","second":"Source-B"}`),
	}
	unbound := &subscriptionSelection{bindings: filepath.Join(dir, "absent.json")}
	deadline := time.Now().Add(10 * time.Second)
	for {
		state, err := selection.group(t.Context())
		if err == nil && state.Now == "Source-A" {
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(log.Name())
			t.Fatalf("isolated Mihomo did not initialize the subscription selector: %v\n%s", err, data)
		}
		time.Sleep(100 * time.Millisecond)
	}

	rawTransport := transport.(controllerTransport).upstream
	rawClient := &http.Client{Transport: rawTransport, Timeout: time.Second}
	denied, err := rawClient.Get(target.String() + "/rules")
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated controller gave %d", denied.StatusCode)
	}
	rules := trafficRules{client: selection.controller, routerURL: target.String()}
	out := httptest.NewRecorder()
	rules.ServeHTTP(out, httptest.NewRequest("GET", trafficRulesPath+"/api/rules", nil))
	if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), subscriptionSelector) {
		t.Fatalf("rules over native HTTPS: %d", out.Code)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/subs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]string{
			{"name": "first", "displayName": "测试订阅 A", "source": "remote"},
			{"name": "second", "displayName": "测试订阅 B", "source": "remote"},
		})
	})
	var pulls atomic.Int32
	mux.HandleFunc("/download/first/JSON", func(w http.ResponseWriter, r *http.Request) {
		switch pulls.Add(1) {
		case 1:
			w.Header().Set("subscription-userinfo", "upload=0; download=1073741824; total=4294967296; expire=946684800")
			w.Write([]byte(`[{"name":"node-1"},{"name":"node-2"}]`))
		case 2:
			http.Error(w, "fixture update failed", http.StatusBadGateway)
		default:
			w.Write([]byte(`[{"name":"node-1"}]`))
		}
	})
	subscriptionBackend := httptest.NewServer(mux)
	defer subscriptionBackend.Close()
	refresh, err := openSubscriptionRefresh(subscriptionBackend.URL, filepath.Join(dir, "subscription-details.json"))
	if err != nil {
		t.Fatal(err)
	}
	dist := os.Getenv("SUBSCRIPTION_TEST_DIST")
	if dist == "" {
		dist = "../subscriptions/dist"
	}
	assets := http.StripPrefix("/subscriptions", http.FileServer(http.Dir(dist)))
	dashboard := httputil.NewSingleHostReverseProxy(target)
	dashboard.Transport = transport
	dashboard.ModifyResponse = selection.filterDashboardProxies
	checkDashboardGroup := func(activeGroup, want, inactiveGroup, inactiveNode string) {
		t.Helper()
		out := httptest.NewRecorder()
		dashboard.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/proxies", nil))
		var state struct{ Proxies map[string]routerSelector }
		if out.Code != http.StatusOK || json.Unmarshal(out.Body.Bytes(), &state) != nil {
			t.Fatalf("dashboard proxies did not load: %d", out.Code)
		}
		if group := state.Proxies[activeGroup]; len(group.All) != 1 || group.All[0] != want {
			t.Fatalf("current subscription options: %+v, want only %s", group, want)
		}
		if _, found := state.Proxies["当前订阅 · 手动切换"]; found {
			t.Fatal("dashboard exposes an extra subscription alias")
		}
		if _, found := state.Proxies[inactiveGroup]; found {
			t.Fatalf("dashboard lists inactive group %s", inactiveGroup)
		}
		for name, group := range state.Proxies {
			for _, option := range group.All {
				if option == inactiveGroup || option == inactiveNode {
					t.Fatalf("dashboard group %s lists inactive option %s", name, option)
				}
			}
		}
		var legacy routerSelector
		if err := selection.callGroup(t.Context(), "Legacy", http.MethodGet, "/group/", nil, http.StatusOK, &legacy); err != nil ||
			legacy.Now != "DIRECT" || len(legacy.All) != 3 {
			t.Fatalf("controller's legacy group changed: %+v, %v", legacy, err)
		}
		providers := httptest.NewRecorder()
		dashboard.ServeHTTP(providers, httptest.NewRequest(http.MethodGet, "/providers/proxies", nil))
		if providers.Code != http.StatusOK || !strings.Contains(providers.Body.String(), inactiveNode) {
			t.Fatal("provider dashboard lost inactive subscription data")
		}
	}
	checkDashboardGroup("Source-A", "Fixture-Node-A", "Source-B", "Fixture-Node-B")
	handler := func(s *subscriptionSelection) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, managementPath+"/") {
				serveManagement(w, r, dist, http.StripPrefix(managementPath, http.FileServer(http.Dir(dist))))
				return
			}
			serveSubscriptions(w, r, "/subscriptions", filepath.Join(dist, "index.html"), assets, mux, s, refresh, true,
				r.Header.Get("Origin") == "http://"+r.Host && r.Header.Get("Origin") != "")
		}
	}
	server := httptest.NewServer(handler(selection))
	defer server.Close()
	// Load the same compiled app with the handler that sees absent bindings.
	unboundServer := httptest.NewServer(handler(unbound))
	defer unboundServer.Close()

	for _, tc := range []struct {
		origin string
		body   string
		want   int
	}{
		{"https://external.invalid", `{"id":"second"}`, http.StatusForbidden},
		{server.URL, `{"id":"unbound"}`, http.StatusConflict},
	} {
		req, err := http.NewRequest(http.MethodPut, server.URL+"/subscriptions/api/active-subscription", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", tc.origin)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.want {
			t.Fatalf("selection returned %d, want %d", response.StatusCode, tc.want)
		}
	}
	cmd := exec.Command("node", browser, "test", "--workers=1")
	cmd.Dir = "../subscriptions"
	cmd.Env = append(os.Environ(), "SUBSCRIPTION_TEST_URL="+server.URL, "SUBSCRIPTION_UNBOUND_URL="+unboundServer.URL)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser regression check failed: %v\n%s", err, output)
	} else {
		t.Logf("%s", output)
	}
	state, err := selection.group(t.Context())
	if err != nil || state.Now != "Source-B" {
		t.Fatalf("Mihomo did not keep Source-B: %v", err)
	}
	checkDashboardGroup("Source-B", "Fixture-Node-B", "Source-A", "Fixture-Node-A")
	checkRuleReloadAndRollback(t, core, selection, path, config)
}
