package rulepublish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type testEngine struct {
	rules    []Rule
	reloads  int
	failOnce bool
	invalid  bool
}

func (m *testEngine) Read(context.Context) ([]Rule, error) { return slices.Clone(m.rules), nil }
func (m *testEngine) Validate(context.Context, string) error {
	if m.invalid {
		return errors.New("validation failure")
	}
	return nil
}
func (m *testEngine) Reload(_ context.Context, raw []byte) error {
	doc, err := readConfig(raw)
	if err != nil {
		return err
	}
	m.rules = nil
	for i, node := range doc.active.Content {
		r, err := parseRule(node.Value, i)
		if err != nil {
			return err
		}
		m.rules = append(m.rules, r)
	}
	m.reloads++
	if m.failOnce {
		m.failOnce = false
		return errors.New("request failed after runtime changed")
	}
	return nil
}

const original = `# initial profile
mixed-port: 7800
rules:
  - IP-CIDR,192.0.2.0/24,DIRECT,no-resolve # keep modifier
  - AND,((NETWORK,udp),(DST-PORT,443)),REJECT
  - MATCH,DIRECT
`

func setup(t *testing.T, engine *testEngine) (Publisher, Request) {
	t.Helper()
	dir := t.TempDir()
	startup := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(startup, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reload(t.Context(), []byte(original)); err != nil {
		t.Fatal(err)
	}
	engine.reloads = 0
	engine.rules[1].Payload = "((Network,udp) && (DstPort,443))"
	p := Publisher{Startup: startup, Backups: filepath.Join(dir, "backups"), Engine: engine}
	read, err := p.Run(t.Context(), Request{Action: "read"})
	if err != nil {
		t.Fatal(err)
	}
	draft := slices.Clone(read.Rules)
	draft[0].Proxy = "REJECT"
	draft[1].Extra.Disabled = true
	added := Rule{Index: 3, Type: "DomainSuffix", Payload: "example.test", Proxy: "DIRECT"}
	draft = append(draft[:2], added, draft[2])
	return p, Request{Action: "preview", Baseline: read.Rules, Rules: draft}
}

func TestPublishAndPersist(t *testing.T) {
	engine := &testEngine{}
	p, req := setup(t, engine)
	preview, err := p.Run(t.Context(), req)
	if err != nil || preview.Ticket == "" || engine.reloads != 0 || !unchanged(p.Startup, []byte(original)) {
		t.Fatalf("preview changed the active config: %v", err)
	}
	req.Action = "apply"
	req.Ticket = "unapproved"
	if _, err = p.Run(t.Context(), req); err == nil || engine.reloads != 0 {
		t.Fatal("unapproved apply passed")
	}
	req.Ticket = preview.Ticket
	result, err := p.Run(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Backup == "" || !unchanged(filepath.Join(p.Backups, result.Backup, "config.yaml"), []byte(original)) {
		t.Fatal("old startup file was not backed up")
	}
	result, err = p.Run(t.Context(), Request{Action: "read"})
	if err != nil || len(result.Rules) != 4 || !result.Rules[1].Extra.Disabled || result.Rules[2].Payload != "example.test" {
		t.Fatalf("reopened configuration did not match the draft: %+v %v", result, err)
	}
	raw, err := os.ReadFile(p.Startup)
	if err != nil || !strings.Contains(string(raw), "IP-CIDR,192.0.2.0/24,REJECT,no-resolve # keep modifier") || !strings.Contains(string(raw), "mixed-port: 7800") {
		t.Fatal("original modifier or unrelated setting was lost")
	}
}

func TestApplyFailureRollsBack(t *testing.T) {
	engine := &testEngine{}
	p, req := setup(t, engine)
	preview, err := p.Run(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Action, req.Ticket = "apply", preview.Ticket
	engine.failOnce = true
	_, err = p.Run(t.Context(), req)
	if err == nil || !strings.Contains(err.Error(), "已恢复原规则和启动配置") {
		t.Fatalf("expected verified rollback: %v", err)
	}
	if engine.reloads != 2 || !unchanged(p.Startup, []byte(original)) || !sameDefinitions(engine.rules, req.Baseline) {
		t.Fatal("rollback lost the original startup or runtime rules")
	}
}

func TestChangedBaselineAndInvalidConfig(t *testing.T) {
	engine := &testEngine{}
	p, req := setup(t, engine)
	engine.invalid = true
	if _, err := p.Run(t.Context(), req); err == nil || engine.reloads != 0 {
		t.Fatal("invalid config mutated runtime")
	}
	engine.invalid = false
	preview, err := p.Run(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Startup, []byte(original+"log-level: warning\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action, req.Ticket = "apply", preview.Ticket
	if _, err = p.Run(t.Context(), req); err == nil || engine.reloads != 0 {
		t.Fatal("changed startup accepted prior preview")
	}
}
