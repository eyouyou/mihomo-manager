package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"time"

	"mihomo-manager/internal/rulepublish"
)

type rulePublisher string

func (publisher rulePublisher) run(ctx context.Context, req rulepublish.Request) (rulepublish.Result, error) {
	timeout := 2 * time.Minute
	if req.Action == "apply" {
		ctx = context.WithoutCancel(ctx)
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := json.Marshal(req)
	if err != nil {
		return rulepublish.Result{}, errors.New("无效的规则发布请求")
	}
	cmd := exec.CommandContext(ctx, string(publisher))
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	var result rulepublish.Result
	if err != nil || len(out) > 16<<20 || json.Unmarshal(out, &result) != nil {
		return rulepublish.Result{}, errors.New("规则发布器没有返回结果，请重新读取现网确认状态")
	}
	return result, nil
}

func (publisher rulePublisher) serve(w http.ResponseWriter, r *http.Request, action string, trustedOrigin bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !trustedOrigin {
		http.Error(w, "Origin mismatch", http.StatusForbidden)
		return
	}
	if publisher == "" {
		http.Error(w, "路由器发布器暂未配置", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req rulepublish.Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var trailing any
	if decoder.Decode(&req) != nil || decoder.Decode(&trailing) != io.EOF || (action == "apply" && len(req.Ticket) != 64) {
		http.Error(w, "Invalid rule draft", http.StatusBadRequest)
		return
	}
	req.Action = action
	result, err := publisher.run(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if result.Error != "" {
		w.WriteHeader(http.StatusConflict)
	}
	_ = json.NewEncoder(w).Encode(result)
}
