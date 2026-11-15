package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
)

// This group is the only controller resource the subscription page can write.
const subscriptionSelector = "Sub-Store 订阅"

func serveSubscriptions(w http.ResponseWriter, r *http.Request, prefix, index string, assets, api http.Handler,
	selection *subscriptionSelection, staticAsset, trustedOrigin bool) {
	suffix := strings.TrimPrefix(r.URL.Path, prefix)
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case suffix == "/api/active-subscription":
		selection.serve(w, r, trustedOrigin)
	case read && suffix == "":
		http.Redirect(w, r, prefix+"/", http.StatusSeeOther)
	case read && suffix == "/":
		http.ServeFile(w, r, index)
	case read && strings.HasPrefix(suffix, "/assets/") && staticAsset:
		assets.ServeHTTP(w, r)
	case strings.HasPrefix(suffix, "/api/") || (read && strings.HasPrefix(suffix, "/download/")):
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
		http.StripPrefix(prefix, api).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

type subscriptionSelection struct {
	controller *http.Client
	routerURL  string
	bindings   string
	mu         sync.Mutex
}

type subscriptionState struct {
	Current   string   `json:"current"`
	Available []string `json:"available"`
}

type routerSelector struct {
	Type string
	Now  string
	All  []string
}

func (s *subscriptionSelection) serve(w http.ResponseWriter, r *http.Request, trustedOrigin bool) {
	var request struct {
		ID string `json:"id"`
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		if !trustedOrigin {
			http.Error(w, "Origin mismatch", http.StatusForbidden)
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || request.ID == "" || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "Invalid subscription", http.StatusBadRequest)
			return
		}
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	bindings := map[string]string{}
	raw, err := os.ReadFile(s.bindings)
	if os.IsNotExist(err) {
		if r.Method == http.MethodPut {
			http.Error(w, "Router subscription selector is not configured", http.StatusServiceUnavailable)
			return
		}
		writeSubscriptionState(w, subscriptionState{Available: []string{}})
		return
	}
	if err != nil || json.Unmarshal(raw, &bindings) != nil || len(bindings) == 0 {
		http.Error(w, "Invalid router subscription bindings", http.StatusServiceUnavailable)
		return
	}
	group, err := s.group(r.Context())
	if err != nil {
		http.Error(w, "Router subscription selector unavailable", http.StatusServiceUnavailable)
		return
	}
	state := subscriptionState{Available: []string{}}
	seen := map[string]bool{}
	for id, choice := range bindings {
		if id == "" || choice == "" || seen[choice] || !slices.Contains(group.All, choice) {
			http.Error(w, "Router subscription bindings mismatch", http.StatusServiceUnavailable)
			return
		}
		seen[choice] = true
		state.Available = append(state.Available, id)
		if group.Now == choice {
			state.Current = id
		}
	}
	slices.Sort(state.Available)
	if r.Method == http.MethodPut {
		choice, ok := bindings[request.ID]
		if !ok {
			http.Error(w, "Subscription is not bound to the router", http.StatusConflict)
			return
		}
		if group.Now != choice {
			payload, _ := json.Marshal(map[string]string{"name": choice})
			err = s.call(r.Context(), http.MethodPut, "/proxies/", payload, http.StatusNoContent, nil)
			if err == nil {
				group, err = s.group(r.Context())
			}
			if err != nil || group.Now != choice {
				http.Error(w, "Router did not confirm the subscription", http.StatusBadGateway)
				return
			}
		}
		state.Current = request.ID
	}
	writeSubscriptionState(w, state)
}

func writeSubscriptionState(w http.ResponseWriter, state subscriptionState) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func (s *subscriptionSelection) group(ctx context.Context) (routerSelector, error) {
	var group routerSelector
	err := s.call(ctx, http.MethodGet, "/group/", nil, http.StatusOK, &group)
	if err == nil && group.Type != "Selector" {
		err = fmt.Errorf("router group is not a selector")
	}
	return group, err
}

func (s *subscriptionSelection) call(ctx context.Context, method, path string, body []byte, status int, result any) error {
	return s.callGroup(ctx, subscriptionSelector, method, path, body, status, result)
}

func (s *subscriptionSelection) callGroup(ctx context.Context, group, method, path string, body []byte, status int, result any) error {
	request, err := http.NewRequestWithContext(ctx, method, s.routerURL+path+url.PathEscape(group), bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.controller.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		return fmt.Errorf("router returned HTTP %d", response.StatusCode)
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result)
	}
	return nil
}
