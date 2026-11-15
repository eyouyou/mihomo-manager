package main

import (
	"crypto/hmac"
	hash256 "crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const ssoFlowCookie = "__Host-mihomo-manager-oidc"
const ssoSessionCookie = "__Host-mihomo-manager-sso"

type dashboardSSO struct {
	clientID        string
	secret          string
	origin          string
	issuer          string
	allowedSubjects []string
	signingKey      []byte
	client          *http.Client
}

func newDashboardSSO(root, origin string, signingKey []byte) *dashboardSSO {
	clientID, err := os.ReadFile(filepath.Join(root, "oidc-client-id"))
	if err != nil || strings.TrimSpace(string(clientID)) == "" {
		return nil
	}
	secret, err := os.ReadFile(filepath.Join(root, "oidc-client-secret"))
	if err != nil || strings.TrimSpace(string(secret)) == "" {
		return nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	issuer := configuredEndpoint("OIDC_ISSUER")
	if issuer.Scheme != "https" {
		return nil
	}
	subjects := strings.Fields(os.Getenv("OIDC_ALLOWED_SUBJECTS"))
	if len(subjects) == 0 {
		return nil
	}
	return &dashboardSSO{
		clientID: strings.TrimSpace(string(clientID)), secret: strings.TrimSpace(string(secret)), origin: origin, signingKey: signingKey,
		issuer: issuer.String(), allowedSubjects: subjects,
		client: &http.Client{Transport: transport, Timeout: 8 * time.Second},
	}
}

func (s *dashboardSSO) signature(value string) string {
	mac := hmac.New(hash256.New, s.signingKey)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *dashboardSSO) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration, sameSite http.SameSite) {
	payload := fmt.Sprintf("%d.%s", time.Now().Add(ttl).Unix(), value)
	http.SetCookie(w, &http.Cookie{Name: name, Value: payload + "." + s.signature(s.origin+"."+name+"."+payload),
		Path: "/", Secure: true, HttpOnly: true, SameSite: sameSite, MaxAge: int(ttl / time.Second)})
}

func (s *dashboardSSO) cookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	i := strings.LastIndexByte(c.Value, '.')
	if i < 0 {
		return ""
	}
	payload, signature := c.Value[:i], c.Value[i+1:]
	if !hmac.Equal([]byte(signature), []byte(s.signature(s.origin+"."+name+"."+payload))) {
		return ""
	}
	stamp, value, found := strings.Cut(payload, ".")
	expiry, err := strconv.ParseInt(stamp, 10, 64)
	if !found || err != nil || time.Now().Unix() >= expiry {
		return ""
	}
	return value
}

func (s *dashboardSSO) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}
	ctx := oidc.ClientContext(r.Context(), s.client)
	provider, err := oidc.NewProvider(ctx, s.issuer)
	if err != nil {
		http.Error(w, "身份验证服务 暂不可用。", 502)
		return
	}
	cfg := oauth2.Config{ClientID: s.clientID, ClientSecret: s.secret, Endpoint: provider.Endpoint(),
		RedirectURL: s.origin + "/sso/callback", Scopes: []string{oidc.ScopeOpenID}}
	if r.URL.Path == "/sso/login" {
		state, verifier, nonce := oauth2.GenerateVerifier(), oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
		next := "/"
		switch destination := r.URL.Query().Get("next"); destination {
		case managementPath + "/", managementRulesPage:
			next = destination
		}
		s.setCookie(w, ssoFlowCookie, strings.Join([]string{state, verifier, nonce, next}, "."), 5*time.Minute, http.SameSiteLaxMode)
		http.Redirect(w, r, cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), http.StatusSeeOther)
		return
	}
	parts := strings.Split(s.cookie(r, ssoFlowCookie), ".")
	if len(parts) != 4 || !hmac.Equal([]byte(parts[0]), []byte(r.URL.Query().Get("state"))) {
		http.Redirect(w, r, "/sso/login", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: ssoFlowCookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	token, err := cfg.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(parts[1]))
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>登录失败</title><p>登录失败。</p><a href="/sso/login">重新登录</a></html>`)
		return
	}
	raw, _ := token.Extra("id_token").(string)
	id, err := provider.Verifier(&oidc.Config{ClientID: s.clientID}).Verify(ctx, raw)
	if err != nil || id.Nonce != parts[2] {
		http.Error(w, "SSO 登录校验失败。", 401)
		return
	}
	if id.Subject != "" && slices.Contains(s.allowedSubjects, id.Subject) {
		s.setCookie(w, ssoSessionCookie, id.Subject, 30*time.Minute, http.SameSiteLaxMode)
		http.Redirect(w, r, parts[3], http.StatusSeeOther)
		return
	}
	http.Error(w, "此账号没有访问权限。", 403)
}
