package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mihomo-manager/internal/rulepublish"
)

type mihomo struct {
	client *http.Client
	bin    string
	dir    string
}

func (m mihomo) request(ctx context.Context, method, path string, body []byte, want int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://mihomo"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		return nil, fmt.Errorf("controller HTTP %d", res.StatusCode)
	}
	const max = 16 << 20
	raw, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil || len(raw) > max {
		return nil, errors.New("invalid controller response")
	}
	return raw, nil
}

func (m mihomo) Read(ctx context.Context) ([]rulepublish.Rule, error) {
	raw, err := m.request(ctx, http.MethodGet, "/rules", nil, 200)
	if err != nil {
		return nil, err
	}
	var res struct{ Rules []rulepublish.Rule }
	if err := json.Unmarshal(raw, &res); err != nil || res.Rules == nil {
		return nil, errors.New("invalid rules response")
	}
	return res.Rules, nil
}

func (m mihomo) Validate(ctx context.Context, path string) error {
	if filter := os.Getenv("RULE_PUBLISHER_STARTUP_FILTER"); filter != "" {
		if !filepath.IsAbs(filter) {
			return errors.New("startup filter requires an absolute path")
		}
		effective, err := os.CreateTemp(m.dir, ".rule-filter-*.yaml")
		if err != nil {
			return err
		}
		if err := effective.Close(); err != nil {
			os.Remove(effective.Name())
			return err
		}
		defer os.Remove(effective.Name())
		if err := exec.CommandContext(ctx, filter, path, effective.Name()).Run(); err != nil {
			return err
		}
		if err := rulepublish.CheckStartupFilter(path, effective.Name()); err != nil {
			return err
		}
	}
	return exec.CommandContext(ctx, m.bin, "-t", "-d", m.dir, "-f", path).Run()
}

func (m mihomo) Reload(ctx context.Context, config []byte) error {
	raw, err := json.Marshal(map[string]string{"payload": string(config)})
	if err != nil {
		return err
	}
	_, err = m.request(ctx, http.MethodPut, "/configs?force=false", raw, 204)
	return err
}

func main() {
	reply := func(result rulepublish.Result) { _ = json.NewEncoder(os.Stdout).Encode(result) }
	fail := func(err error) { reply(rulepublish.Result{Error: err.Error()}) }
	if len(os.Args) != 5 {
		fail(errors.New("规则发布器缺少启动参数"))
		return
	}
	startup, socket, binary, backups := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	for _, p := range []string{startup, socket, binary, backups} {
		if !filepath.IsAbs(p) {
			fail(errors.New("规则发布路径必须为绝对路径"))
			return
		}
	}
	var request rulepublish.Request
	const max = 4 << 20
	input, err := io.ReadAll(io.LimitReader(os.Stdin, max+1))
	if err != nil || len(input) > max || json.Unmarshal(input, &request) != nil {
		fail(errors.New("无效的规则发布请求"))
		return
	}
	dialer := net.Dialer{Timeout: 4 * time.Second}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socket)
	}}
	transport.ResponseHeaderTimeout = 100 * time.Second
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	engine := mihomo{client: client, bin: binary, dir: filepath.Dir(startup)}
	p := rulepublish.Publisher{Startup: startup, Backups: backups, Engine: engine}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := p.Run(ctx, request)
	if err != nil {
		fail(err)
		return
	}
	reply(result)
}
