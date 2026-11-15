package main

import (
	_ "embed"
	"encoding/hex"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

//go:embed dashboard-subscriptions.html
var dashboardSubscriptions string

func main() {
	if len(os.Args) != 2 {
		log.Fatal("expected credential directory")
	}
	encodedKey, err := os.ReadFile(filepath.Join(os.Args[1], "session-signing-key"))
	if err != nil {
		log.Fatal(err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(encodedKey)))
	if err != nil || len(key) != 32 {
		log.Fatal("invalid session signing key: expected 32 bytes")
	}
	mux := http.NewServeMux()
	root := os.Getenv("MANAGER_DATA_DIR")
	if root == "" {
		root = "."
	}
	root = filepath.ToSlash(filepath.Clean(root)) + "/"
	const subscriptionPath = "/subscriptions"
	managerOrigin := configuredOrigin("MANAGER_ORIGIN")
	dashboardHost := managerOrigin.Host
	router := configuredOrigin("MIHOMO_CONTROLLER_URL")
	subscriptionTarget := configuredEndpoint("SUBSCRIPTION_BACKEND_URL")
	hosts := map[string]bool{dashboardHost: true}
	if os.Getenv("SUBSCRIPTION_ALIAS_ORIGIN") != "" {
		alias := configuredOrigin("SUBSCRIPTION_ALIAS_ORIGIN")
		if alias.Host == dashboardHost {
			log.Fatal("SUBSCRIPTION_ALIAS_ORIGIN must use a separate host")
		}
		hosts[alias.Host] = false
	}
	managementUI := http.StripPrefix(managementPath, http.FileServer(http.Dir(root+"management-ui")))
	transport, err := routerControllerTransport(os.Args[1], router)
	if err != nil {
		log.Fatal(err)
	}
	dashboardAssets := http.StripPrefix("/ui", http.FileServer(http.Dir(root+"zashboard/dist")))
	subscriptionAPI := httputil.NewSingleHostReverseProxy(subscriptionTarget)
	selection := subscriptionSelection{
		controller: &http.Client{Transport: transport, Timeout: 6 * time.Second},
		routerURL:  router.String(),
		bindings:   filepath.Join(os.Args[1], "subscription-selectors.json"),
	}
	rulesClient := *selection.controller
	rulesClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	rules := trafficRules{client: &rulesClient, routerURL: selection.routerURL, directory: root + "management-ui"}
	dashboardIndex, err := os.ReadFile(root + "zashboard/dist/index.html")
	if err != nil {
		log.Fatal(err)
	}
	dashboardHTML := strings.Replace(string(dashboardIndex), "</body>", dashboardSubscriptions+"</body>", 1)
	for host, primary := range hosts {
		origin := "https://" + host
		sso := newDashboardSSO(os.Args[1], origin, key)
		if sso == nil {
			log.Fatal("OIDC client credential missing")
		}
		proxy := httputil.NewSingleHostReverseProxy(subscriptionTarget)
		if primary {
			proxy = httputil.NewSingleHostReverseProxy(router)
			proxy.Transport = transport
			direct := proxy.Director
			proxy.Director = func(r *http.Request) {
				direct(r)
				if r.Method == http.MethodGet && r.URL.Path == "/proxies" {
					r.Header.Del("Accept-Encoding")
				}
			}
			proxy.ModifyResponse = selection.filterDashboardProxies
		}
		mux.Handle(host+"/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "same-origin")
			proto := r.Header.Get("X-Forwarded-Proto")
			websocket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
			if r.Host != host || (proto != "https" && !(proto == "wss" && websocket)) {
				http.Error(w, "HTTPS required", 403)
				return
			}
			p := r.URL.Path
			if primary && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				switch p {
				case subscriptionPath, subscriptionPath + "/", managementPath:
					http.Redirect(w, r, managementPath+"/", http.StatusSeeOther)
					return
				case trafficRulesPath, trafficRulesPath + "/":
					http.Redirect(w, r, managementRulesPage, http.StatusSeeOther)
					return
				}
			}
			read := r.Method == "GET" || r.Method == "HEAD"
			trafficRulesPage := primary && (p == trafficRulesPath || strings.HasPrefix(p, trafficRulesPath+"/"))
			subscriptions := primary && (p == subscriptionPath || strings.HasPrefix(p, subscriptionPath+"/"))
			if read && ((!primary && p == "/sw.js") || (primary && p == "/ui/sw.js")) {
				// Retire the upstream worker so it cannot serve a cached app shell
				// when the gateway needs to start a new SSO session.
				w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
				w.Write([]byte(`self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', event => event.waitUntil((async () => {
  await self.clients.claim();
  await self.registration.unregister();
})()));
`))
				return
			}
			if !primary && read && (p == "/" || p == "/subs" || strings.HasPrefix(p, "/sso/")) {
				http.Redirect(w, r, "https://"+dashboardHost+managementPath+"/", http.StatusSeeOther)
				return
			}
			if p == "/sso/login" || p == "/sso/callback" {
				sso.serve(w, r)
				return
			}
			if sso.cookie(r, ssoSessionCookie) == "" {
				if read && primary && p == managementPath+"/" {
					destination := managementPath + "/"
					if r.URL.Query().Get("view") == "rules" {
						destination = managementRulesPage
					}
					http.Redirect(w, r, "/sso/login?next="+url.QueryEscape(destination), http.StatusSeeOther)
				} else if read && (r.Header.Get("Sec-Fetch-Mode") == "navigate" || p == "/" || p == "/ui/") {
					http.Redirect(w, r, "/sso/login", 303)
				} else {
					http.Error(w, "Authentication required", 401)
				}
				return
			}
			requestOrigin := r.Header.Get("Origin")
			referer, _ := url.Parse(r.Referer())
			sameOriginReferer := referer != nil && referer.Scheme == "https" && referer.Host == host
			trustedOrigin := requestOrigin == origin ||
				((!primary || subscriptions) &&
					(requestOrigin == "" || requestOrigin == "null") && sameOriginReferer)
			staticAsset := (subscriptions && read && strings.HasPrefix(p, subscriptionPath+"/assets/") && path.Clean(p) == p) ||
				(!primary && read &&
					(p == "/index.js" || (path.Clean(p) == p && path.Ext(p) != "" &&
						(strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/chunks/") || strings.HasPrefix(p, "/fonts/") || strings.HasPrefix(p, "/css/")))))
			if (requestOrigin != "" && !trustedOrigin && !staticAsset) ||
				((websocket || (r.Method != "GET" && r.Method != "HEAD")) && !trustedOrigin) {
				log.Printf("origin check rejected host=%s method=%s null_origin=%t origin_present=%t same_origin_referrer=%t websocket=%t",
					host, r.Method, requestOrigin == "null", requestOrigin != "", sameOriginReferer, websocket)
				http.Error(w, "Origin mismatch", 403)
				return
			}
			if staticAsset {
				r.Header.Del("Origin")
			} else if (!primary || subscriptions) && trustedOrigin && requestOrigin != origin {
				r.Header.Set("Origin", origin)
			}
			if primary && (p == managementPath+"/" || strings.HasPrefix(p, managementPath+"/")) {
				serveManagement(w, r, root+"management-ui", managementUI)
				return
			}
			if trafficRulesPage {
				rules.ServeHTTP(w, r)
				return
			}
			if subscriptions {
				serveSubscriptions(w, r, subscriptionPath, root+"management-ui/index.html",
					managementUI, subscriptionAPI, &selection, staticAsset, trustedOrigin)
				return
			}
			if primary {
				if p == "/" {
					http.Redirect(w, r, "/ui/", 303)
					return
				}
				if read && (p == "/ui/" || p == "/ui/index.html") {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Write([]byte(dashboardHTML))
					return
				}
				if read && p == "/ui/registerSW.js" {
					w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
					w.Write([]byte("navigator.serviceWorker?.getRegistration().then(r=>r?.update()).catch(()=>{});"))
					return
				}
				if read && strings.HasPrefix(p, "/ui/") {
					dashboardAssets.ServeHTTP(w, r)
					return
				}
				if !zashboardAllowed(r.Method, p) {
					http.Error(w, "Operation not enabled", 403)
					return
				}
				if proxyDelayPath(p) || providerNodeCheck(p) {
					// Keep the controller's probe limited to the dashboard's known test endpoints.
					query, err := url.ParseQuery(r.URL.RawQuery)
					target := query.Get("url")
					if err != nil || (target != "https://www.gstatic.com/generate_204" &&
						target != "https://www.google.com/generate_204") {
						http.Error(w, "Unsupported latency test URL", http.StatusBadRequest)
						return
					}
					r.URL.RawQuery = url.Values{"url": {target}, "timeout": {"5000"}}.Encode()
				}
			}
			if !primary && !(strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/download/")) {
				http.NotFound(w, r)
				return
			}
			r.Header.Del("Cookie")
			r.Header.Del("Authorization")
			proxy.ServeHTTP(w, r)
		}))
	}
	listen := os.Getenv("GATEWAY_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}

func configuredOrigin(name string) *url.URL {
	target := configuredEndpoint(name)
	if target.Path == "/" {
		target.Path = ""
	}
	if target.Scheme != "https" || target.Path != "" {
		log.Fatalf("%s requires an HTTPS origin", name)
	}
	return target
}

func configuredEndpoint(name string) *url.URL {
	target, err := url.Parse(os.Getenv(name))
	if err != nil || target.Hostname() == "" || (target.Scheme != "http" && target.Scheme != "https") ||
		target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		log.Fatalf("invalid %s", name)
	}
	return target
}

func zashboardAllowed(method, path string) bool {
	if method == http.MethodPut {
		group, ok := strings.CutPrefix(path, "/proxies/")
		return ok && group != "" && group != "." && group != ".." && !strings.Contains(group, "/")
	}
	if method != "GET" && method != "HEAD" {
		return false
	}
	if method == http.MethodGet && (proxyDelayPath(path) || providerNodeCheck(path)) {
		return true
	}
	if strings.HasPrefix(path, "/ui/") {
		return true
	}
	switch path {
	case "/version", "/configs", "/proxies", "/group", "/rules", "/providers/proxies", "/providers/rules", "/connections", "/traffic", "/memory", "/logs":
		return true
	}
	for _, prefix := range []string{"/group/", "/proxies/", "/providers/proxies/", "/providers/rules/"} {
		if suffix, ok := strings.CutPrefix(path, prefix); ok && suffix != "" && !strings.Contains(suffix, "/") {
			return true
		}
	}
	return false
}

func proxyDelayPath(path string) bool {
	for _, prefix := range []string{"/proxies/", "/group/"} {
		suffix, ok := strings.CutPrefix(path, prefix)
		name, delay := strings.CutSuffix(suffix, "/delay")
		if ok && delay && name != "" && name != "." && name != ".." && !strings.Contains(name, "/") {
			return true
		}
	}
	return false
}

func providerNodeCheck(p string) bool {
	name, ok := strings.CutPrefix(p, "/providers/proxies/")
	if !ok {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[2] != "healthcheck" {
		return false
	}
	for _, part := range parts[:2] {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
