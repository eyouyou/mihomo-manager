package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
)

func (s *subscriptionSelection) dashboardBindings() map[string]string {
	raw, err := os.ReadFile(s.bindings)
	var bindings map[string]string
	if err != nil || json.Unmarshal(raw, &bindings) != nil {
		return nil
	}
	return bindings
}

func selectedSubscriptionChild(parent routerSelector, bindings map[string]string) string {
	if parent.Type != "Selector" || !slices.Contains(parent.All, parent.Now) {
		return ""
	}
	for _, child := range bindings {
		if child != "" && child != subscriptionSelector && child == parent.Now {
			return child
		}
	}
	return ""
}

func (s *subscriptionSelection) filterDashboardProxies(response *http.Response) error {
	if response.Request.Method != http.MethodGet || response.Request.URL.Path != "/proxies" || response.StatusCode != http.StatusOK {
		return nil
	}
	// Read the parent and child from a single controller snapshot.
	defer response.Body.Close()
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&payload); err != nil {
		return err
	}
	var proxies map[string]json.RawMessage
	if err := json.Unmarshal(payload["proxies"], &proxies); err != nil {
		return err
	}
	var parent routerSelector
	if json.Unmarshal(proxies[subscriptionSelector], &parent) == nil {
		bindings := s.dashboardBindings()
		active := selectedSubscriptionChild(parent, bindings)
		var activeGroup routerSelector
		if active != "" && json.Unmarshal(proxies[active], &activeGroup) == nil && activeGroup.Type == "Selector" {
			if err := filterInactiveSubscriptionGroups(proxies, bindings, active, activeGroup.All); err != nil {
				return err
			}
			var err error
			payload["proxies"], err = json.Marshal(proxies)
			if err != nil {
				return err
			}
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.Header.Set("Content-Length", strconv.Itoa(len(data)))
	response.Header.Del("ETag")
	return nil
}

// This filters only the proxy dashboard's read response. The provider tab and
// the controller's routing state retain their own unmodified data.
func filterInactiveSubscriptionGroups(proxies map[string]json.RawMessage, bindings map[string]string, active string, activeNodes []string) error {
	hidden := map[string]bool{}
	for _, child := range bindings {
		if child == active || child == subscriptionSelector {
			continue
		}
		var group routerSelector
		if json.Unmarshal(proxies[child], &group) != nil || group.Type != "Selector" {
			continue
		}
		hidden[child] = true
		for _, node := range group.All {
			if !slices.Contains(activeNodes, node) {
				hidden[node] = true
			}
		}
	}
	for name, data := range proxies {
		if hidden[name] {
			delete(proxies, name)
			continue
		}
		var group routerSelector
		if json.Unmarshal(data, &group) != nil || len(group.All) == 0 {
			continue
		}
		candidates := slices.DeleteFunc(group.All, func(name string) bool { return hidden[name] })
		if len(candidates) == len(group.All) {
			continue
		}
		var display map[string]json.RawMessage
		if err := json.Unmarshal(data, &display); err != nil {
			return err
		}
		display["all"], _ = json.Marshal(candidates)
		updated, err := json.Marshal(display)
		if err != nil {
			return err
		}
		proxies[name] = updated
	}
	return nil
}
