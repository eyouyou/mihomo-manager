package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	subscriptionDetailsPath = "/api/subscription-details"
	maxSubscriptionInterval = 30 * 24 * 60
	maxSubscriptionBytes    = 32 << 20
	maxSafeSubscriptionInt  = 1<<53 - 1
)

type subscriptionSnapshot struct {
	Count     int    `json:"count"`
	UpdatedAt int64  `json:"updatedAt"`
	Used      *int64 `json:"used,omitempty"`
	Total     int64  `json:"total,omitempty"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

type subscriptionDetails struct {
	Snapshot        *subscriptionSnapshot `json:"snapshot,omitempty"`
	IntervalMinutes int                   `json:"intervalMinutes"`
	NextUpdateAt    int64                 `json:"nextUpdateAt,omitempty"`
	LastError       string                `json:"lastError,omitempty"`
	Updating        bool                  `json:"updating,omitempty"`
}

type subscriptionRecord struct {
	Fingerprint string              `json:"fingerprint"`
	Details     subscriptionDetails `json:"details"`
}

type subscriptionSource struct {
	remote      bool
	fingerprint string
}

type subscriptionRefresh struct {
	upstream string
	client   *http.Client
	file     string

	mu       sync.Mutex
	records  map[string]subscriptionRecord
	inFlight map[string]bool
}

func openSubscriptionRefresh(upstream, file string) (*subscriptionRefresh, error) {
	s := &subscriptionRefresh{
		upstream: strings.TrimRight(upstream, "/"),
		client: &http.Client{
			Timeout:       65 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		file: file, records: map[string]subscriptionRecord{}, inFlight: map[string]bool{},
	}
	data, err := os.ReadFile(file)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &s.records); err != nil || s.records == nil {
			return nil, errors.New("invalid subscription refresh state")
		}
	case !os.IsNotExist(err):
		return nil, err
	default:
		if err := s.commit(s.records); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Call with mu held after startup. Publish the new state only after the file is replaced.
func (s *subscriptionRefresh) commit(records map[string]subscriptionRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.file), ".subscription-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(file.Name(), s.file); err != nil {
		return err
	}
	s.records = records
	return nil
}

func (s *subscriptionRefresh) fetch(ctx context.Context, path string) (json.RawMessage, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.upstream+path, nil)
	if err != nil {
		return nil, nil, err
	}
	response, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("subscription backend HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSubscriptionBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > maxSubscriptionBytes || !json.Valid(body) {
		return nil, nil, errors.New("invalid subscription backend response")
	}
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		var envelope struct {
			Status string          `json:"status"`
			Data   json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Status != "success" {
			return nil, nil, errors.New("subscription backend rejected request")
		}
		body = envelope.Data
	}
	return body, response.Header, nil
}

func (s *subscriptionRefresh) catalog(ctx context.Context) (map[string]subscriptionSource, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, _, err := s.fetch(ctx, "/api/subs")
	if err != nil {
		return nil, err
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(data, &items) != nil || items == nil {
		return nil, errors.New("invalid subscription catalog")
	}
	sources := make(map[string]subscriptionSource, len(items))
	for _, item := range items {
		var name, source string
		if json.Unmarshal(item["name"], &name) != nil || name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			continue
		}
		if json.Unmarshal(item["source"], &source) != nil || (source != "local" && source != "remote") {
			continue
		}
		for _, key := range []string{"displayName", "display-name", "icon", "isIconColor", "remark", "tag", "subscriptionTags", "form"} {
			delete(item, key)
		}
		identity, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		sources[name] = subscriptionSource{remote: source == "remote", fingerprint: fmt.Sprintf("%x", sha256.Sum256(identity))}
	}
	return sources, nil
}

func (r subscriptionRecord) visible(source subscriptionSource) subscriptionDetails {
	result := r.Details
	if r.Fingerprint != source.fingerprint {
		result.Snapshot = nil
		result.LastError = ""
	}
	if !source.remote {
		result.IntervalMinutes = 0
		result.NextUpdateAt = 0
	}
	return result
}

func (s *subscriptionRefresh) pull(ctx context.Context, name string) (*subscriptionSnapshot, error) {
	data, headers, err := s.fetch(ctx, "/download/"+url.PathEscape(name)+"/JSON?noCache=true")
	if err != nil {
		return nil, err
	}
	var nodes []json.RawMessage
	if json.Unmarshal(data, &nodes) != nil || len(nodes) == 0 {
		return nil, errors.New("subscription contains no nodes")
	}
	snapshot := subscriptionSnapshot{Count: len(nodes), UpdatedAt: time.Now().UnixMilli()}
	fields := map[string]int64{}
	for _, pair := range strings.FieldsFunc(headers.Get("subscription-userinfo"), func(r rune) bool { return r == ';' || r == ',' }) {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil && n >= 0 && n <= maxSafeSubscriptionInt {
			fields[strings.ToLower(strings.TrimSpace(key))] = n
		}
	}
	upload, hasUpload := fields["upload"]
	download, hasDownload := fields["download"]
	if used := upload + download; hasUpload && hasDownload && used <= maxSafeSubscriptionInt {
		snapshot.Used = &used
	}
	snapshot.Total = fields["total"]
	if expire := fields["expire"]; expire <= 8640000000000 {
		snapshot.ExpiresAt = expire * 1000
	}
	return &snapshot, nil
}

func (s *subscriptionRefresh) refresh(ctx context.Context, name string, source subscriptionSource, scheduled bool) (subscriptionDetails, int) {
	s.mu.Lock()
	current := s.records[name].visible(source)
	if scheduled && (current.NextUpdateAt == 0 || current.NextUpdateAt > time.Now().UnixMilli()) {
		s.mu.Unlock()
		return current, http.StatusOK
	}
	if s.inFlight[name] {
		s.mu.Unlock()
		current.Updating = true
		return current, http.StatusOK
	}
	s.inFlight[name] = true
	s.mu.Unlock()

	snapshot, pullErr := s.pull(ctx, name)
	// Check the source again so a concurrent edit or deletion cannot republish stale data.
	sources, catalogErr := s.catalog(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer delete(s.inFlight, name)
	if catalogErr != nil {
		pullErr = catalogErr
	} else if sources[name] != source {
		return subscriptionDetails{}, http.StatusConflict
	}

	current = s.records[name].visible(source)
	if pullErr != nil {
		current.LastError = "订阅源未返回可用节点，请检查来源后重试。"
	} else {
		current.Snapshot = snapshot
		current.LastError = ""
	}
	if current.IntervalMinutes > 0 {
		current.NextUpdateAt = time.Now().Add(time.Duration(current.IntervalMinutes) * time.Minute).UnixMilli()
	}
	records := maps.Clone(s.records)
	records[name] = subscriptionRecord{Fingerprint: source.fingerprint, Details: current}
	if err := s.commit(records); err != nil {
		log.Print("could not persist subscription refresh result")
		return subscriptionDetails{}, http.StatusInternalServerError
	}
	return current, http.StatusOK
}

func (s *subscriptionRefresh) due(ctx context.Context) {
	s.mu.Lock()
	now := time.Now().UnixMilli()
	due := make(map[string]int64)
	for name, record := range s.records {
		at := record.Details.NextUpdateAt
		if at > 0 && at <= now && !s.inFlight[name] {
			due[name] = at
		}
	}
	s.mu.Unlock()
	if len(due) == 0 {
		return
	}
	sources, err := s.catalog(ctx)
	if err != nil {
		return
	}
	for name, at := range due {
		if source, exists := sources[name]; exists && source.remote {
			s.refresh(ctx, name, source, true)
		} else {
			s.removeExpiredSchedule(name, at)
		}
	}
}

func (s *subscriptionRefresh) removeExpiredSchedule(name string, dueAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[name] || s.records[name].Details.NextUpdateAt != dueAt {
		return
	}
	records := maps.Clone(s.records)
	delete(records, name)
	if err := s.commit(records); err != nil {
		log.Print("could not remove obsolete subscription schedule")
	}
}

func (s *subscriptionRefresh) run(ctx context.Context) {
	s.due(ctx)
	timer := time.NewTicker(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.due(ctx)
		}
	}
}

func (s *subscriptionRefresh) serve(w http.ResponseWriter, r *http.Request, suffix string, trustedOrigin bool) {
	w.Header().Set("Cache-Control", "no-store")
	var method string
	switch suffix {
	case subscriptionDetailsPath:
		method = http.MethodGet
	case subscriptionDetailsPath + "/refresh":
		method = http.MethodPost
	case subscriptionDetailsPath + "/schedule":
		method = http.MethodPut
	default:
		http.NotFound(w, r)
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Name            string `json:"name"`
		IntervalMinutes *int   `json:"intervalMinutes"`
	}
	if method != http.MethodGet {
		if !trustedOrigin {
			http.Error(w, "Origin mismatch", http.StatusForbidden)
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || input.Name == "" || decoder.Decode(new(any)) != io.EOF ||
			(method == http.MethodPut && (input.IntervalMinutes == nil || *input.IntervalMinutes < 0 || *input.IntervalMinutes > maxSubscriptionInterval)) {
			http.Error(w, "Invalid subscription update", http.StatusBadRequest)
			return
		}
	}
	sources, err := s.catalog(r.Context())
	if err != nil {
		http.Error(w, "Subscription catalog unavailable", http.StatusBadGateway)
		return
	}
	if method == http.MethodGet {
		result := make(map[string]subscriptionDetails, len(sources))
		s.mu.Lock()
		for name, source := range sources {
			details := s.records[name].visible(source)
			details.Updating = s.inFlight[name]
			result[name] = details
		}
		s.mu.Unlock()
		writeSubscriptionDetails(w, result)
		return
	}
	source, exists := sources[input.Name]
	if !exists {
		http.NotFound(w, r)
		return
	}
	if method == http.MethodPost {
		details, status := s.refresh(context.WithoutCancel(r.Context()), input.Name, source, false)
		if status != http.StatusOK {
			http.Error(w, "Subscription refresh failed", status)
			return
		}
		writeSubscriptionDetails(w, details)
		return
	}
	if !source.remote {
		http.Error(w, "Schedule requires a remote subscription", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	details := s.records[input.Name].visible(source)
	interval := *input.IntervalMinutes
	if details.IntervalMinutes != interval {
		details.IntervalMinutes = interval
		details.NextUpdateAt = 0
		if interval > 0 {
			details.NextUpdateAt = time.Now().Add(time.Duration(interval) * time.Minute).UnixMilli()
		}
		records := maps.Clone(s.records)
		records[input.Name] = subscriptionRecord{Fingerprint: source.fingerprint, Details: details}
		if err := s.commit(records); err != nil {
			http.Error(w, "Could not persist schedule", http.StatusInternalServerError)
			return
		}
	}
	details.Updating = s.inFlight[input.Name]
	writeSubscriptionDetails(w, details)
}

func writeSubscriptionDetails(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
