package rulepublish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Request struct {
	Action   string `json:"action"`
	Baseline []Rule `json:"baseline,omitempty"`
	Rules    []Rule `json:"rules"`
	Ticket   string `json:"ticket,omitempty"`
}

type Result struct {
	Rules  []Rule `json:"rules"`
	Ticket string `json:"ticket,omitempty"`
	Backup string `json:"backup,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Engine interface {
	Read(context.Context) ([]Rule, error)
	Validate(context.Context, string) error
	Reload(context.Context, []byte) error
}

type Publisher struct {
	Startup string
	Backups string
	Engine  Engine
}

func (p Publisher) Run(ctx context.Context, request Request) (Result, error) {
	if request.Action != "read" && request.Action != "preview" && request.Action != "apply" {
		return Result{}, errors.New("未知发布操作")
	}
	original, err := os.ReadFile(p.Startup)
	if err != nil {
		return Result{}, errors.New("无法读取启动配置")
	}
	doc, err := readConfig(original)
	if err != nil {
		return Result{}, err
	}
	live, err := p.Engine.Read(ctx)
	if err != nil {
		return Result{}, errors.New("无法读取运行规则")
	}
	rows, err := doc.mergeRuntime(live)
	if err != nil {
		return Result{}, err
	}
	if request.Action == "read" {
		return Result{Rules: rows}, nil
	}
	if request.Rules == nil || len(request.Rules) > 20000 {
		return Result{}, errors.New("草稿规则数量无效")
	}
	if !sameDefinitions(rows, request.Baseline) {
		return Result{}, errors.New("现网或启动配置已变化，请重新读取规则")
	}
	candidate, wanted, err := doc.candidate(rows, request.Rules)
	if err != nil {
		return Result{}, err
	}
	fingerprint, err := json.Marshal(struct {
		Original  []byte
		Candidate []byte
		Current   []Rule
	}{
		original, candidate, canonicalRules(live),
	})
	if err != nil {
		return Result{}, err
	}
	digest := sha256.Sum256(fingerprint)
	ticket := hex.EncodeToString(digest[:])
	if request.Action == "apply" && request.Ticket != ticket {
		return Result{}, errors.New("候选或基线已变化，请重新校验和预览")
	}
	tmp, err := stage(filepath.Dir(p.Startup), candidate)
	if err != nil {
		return Result{}, errors.New("无法暂存候选配置")
	}
	defer os.Remove(tmp)
	if err := p.Engine.Validate(ctx, tmp); err != nil {
		return Result{}, errors.New("Mihomo 未通过候选配置校验")
	}
	if request.Action == "preview" {
		return Result{Ticket: ticket}, nil
	}

	latest, err := p.Engine.Read(ctx)
	if err != nil || !sameDefinitions(live, latest) || !unchanged(p.Startup, original) {
		return Result{}, errors.New("校验期间现网发生变化，请重新预览")
	}
	if err := os.MkdirAll(p.Backups, 0700); err != nil {
		return Result{}, errors.New("无法创建回滚目录")
	}
	dir, err := os.MkdirTemp(p.Backups, "rule-change-")
	if err != nil {
		return Result{}, errors.New("无法创建回滚版本")
	}
	saved, err := stage(dir, original)
	if err != nil {
		return Result{}, errors.New("无法备份启动配置")
	}
	defer os.Remove(saved)
	if err := os.Rename(saved, filepath.Join(dir, "config.yaml")); err != nil {
		return Result{}, errors.New("无法提交启动配置备份")
	}
	if err := syncDir(dir); err != nil {
		return Result{}, errors.New("无法持久保存启动配置备份")
	}
	if err := syncDir(p.Backups); err != nil {
		return Result{}, errors.New("无法持久保存回滚目录")
	}
	backup := filepath.Base(dir)

	// A browser disconnect must not abort a confirmed transaction midway.
	operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	diskRestored := true
	reloadErr := p.Engine.Reload(operation, candidate)
	if reloadErr == nil {
		current, readErr := p.Engine.Read(operation)
		if readErr != nil || !sameDefinitions(wanted, current) {
			reloadErr = errors.New("热加载后的规则与候选不一致")
		}
	}
	if reloadErr == nil && !unchanged(p.Startup, original) {
		reloadErr = errors.New("应用期间启动配置已变化")
	}
	if reloadErr == nil {
		reloadErr = os.Rename(tmp, p.Startup)
		if reloadErr == nil {
			if err = syncDir(filepath.Dir(p.Startup)); err == nil {
				return Result{Backup: backup}, nil
			}
			// The rename succeeded. Restore the disk before restoring runtime.
			restore, stageErr := stage(filepath.Dir(p.Startup), original)
			if stageErr == nil {
				defer os.Remove(restore)
				stageErr = os.Rename(restore, p.Startup)
				if stageErr == nil {
					stageErr = syncDir(filepath.Dir(p.Startup))
				}
			}
			diskRestored = stageErr == nil
			reloadErr = err
		}
	}

	rollback, cancelRollback := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancelRollback()
	if err := p.Engine.Reload(rollback, original); err != nil {
		return Result{}, fmt.Errorf("发布失败，自动回滚未完成。备份 %s", backup)
	}
	restored, err := p.Engine.Read(rollback)
	if !diskRestored || err != nil || !sameDefinitions(live, restored) || !unchanged(p.Startup, original) {
		return Result{}, fmt.Errorf("发布失败，回滚结果无法确认。备份 %s", backup)
	}
	return Result{}, fmt.Errorf("发布失败，已恢复原规则和启动配置。备份 %s", backup)
}

func canonicalRules(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	for i, rule := range rules {
		out[i] = canonical(rule)
	}
	return out
}

func unchanged(path string, expected []byte) bool {
	data, err := os.ReadFile(path)
	return err == nil && string(data) == string(expected)
}

func stage(dir string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, ".rule-config-*.yaml")
	if err != nil {
		return "", err
	}
	path := file.Name()
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func syncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
