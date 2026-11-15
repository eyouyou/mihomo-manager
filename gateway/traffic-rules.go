package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"mihomo-manager/internal/rulepublish"
)

const trafficRulesPath = "/traffic-rules"

type trafficRules struct {
	publisher rulePublisher
	client    *http.Client
	routerURL string
	directory string
}

func (rules trafficRules) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	page := r.URL.Path
	switch {
	case page == trafficRulesPath+"/api/rules":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rules.snapshot(w, r)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		switch {
		case page == trafficRulesPath:
			http.Redirect(w, r, trafficRulesPath+"/", http.StatusSeeOther)
		case page == trafficRulesPath+"/":
			http.ServeFile(w, r, rules.directory+"/index.html")
		case strings.HasPrefix(page, trafficRulesPath+"/assets/") && path.Clean(page) == page:
			asset := rules.directory + strings.TrimPrefix(page, trafficRulesPath)
			info, err := os.Stat(asset)
			if err != nil || !info.Mode().IsRegular() {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, asset)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (rules trafficRules) snapshot(w http.ResponseWriter, r *http.Request) {
	if rules.publisher != "" {
		result, err := rules.publisher.run(r.Context(), rulepublish.Request{Action: "read"})
		if err == nil && result.Error == "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Rules      []rulepublish.Rule `json:"rules"`
				CanPublish bool               `json:"canPublish"`
			}{result.Rules, true})
			return
		}
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rules.routerURL+"/rules", nil)
	if err != nil {
		http.Error(w, "Rules unavailable", http.StatusBadGateway)
		return
	}
	response, err := rules.client.Do(request)
	if err != nil {
		http.Error(w, "Rules unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "Rules unavailable", http.StatusBadGateway)
		return
	}
	const maxSnapshot = 16 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSnapshot+1))
	var snapshot struct{ Rules []json.RawMessage }
	if err != nil || len(body) > maxSnapshot || json.Unmarshal(body, &snapshot) != nil || snapshot.Rules == nil {
		http.Error(w, "Invalid rules response", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
