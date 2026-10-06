// Package auth implements Google OAuth for Gmail: the installed-app loopback
// flow (with PKCE) to add an account, and keychain-backed token persistence
// that every later Gmail API call goes through.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yoann/pigeon/internal/config"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Scopes: gmail.modify = read, compose, send, label, archive and trash.
// It deliberately excludes permanent deletion (that needs full https://mail.google.com/).
var Scopes = []string{"https://www.googleapis.com/auth/gmail.modify"}

const keyringService = "pigeon"

// ErrNoCredentials means `pigeon setup` has not been run yet.
var ErrNoCredentials = errors.New("no OAuth client configured")

// InstallCredentials validates a Google "Desktop app" client JSON and copies it into the config dir.
func InstallCredentials(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	var probe struct {
		Installed *json.RawMessage `json:"installed"`
		Web       *json.RawMessage `json:"web"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", fmt.Errorf("not a Google OAuth client JSON: %w", err)
	}
	if probe.Installed == nil {
		if probe.Web != nil {
			return "", errors.New(`this is a "Web application" client; create a "Desktop app" OAuth client instead`)
		}
		return "", errors.New(`missing "installed" section; download the JSON of a "Desktop app" OAuth client`)
	}
	if _, err := google.ConfigFromJSON(data, Scopes...); err != nil {
		return "", err
	}
	dst, err := config.CredentialsPath()
	if err != nil {
		return "", err
	}
	return dst, config.WriteFileAtomic(dst, data, 0o600)
}

func oauthConfig() (*oauth2.Config, error) {
	p, err := config.CredentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoCredentials
	}
	if err != nil {
		return nil, err
	}
	return google.ConfigFromJSON(data, Scopes...)
}

// Login runs the browser consent flow and returns the authorized email + token.
// loginHint pre-selects an account in Google's chooser (may be empty).
func Login(ctx context.Context, loginHint string, logf func(string, ...any)) (string, *oauth2.Token, error) {
	cfg, err := oauthConfig()
	if err != nil {
		return "", nil, err
	}

	// Loopback redirect on a random free port (Google allows any port for Desktop clients).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	defer ln.Close()
	cfg.RedirectURL = fmt.Sprintf("http://%s/callback", ln.Addr())

	state := randomString()
	verifier := oauth2.GenerateVerifier()
	opts := []oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,                                   // we want a refresh token
		oauth2.SetAuthURLParam("prompt", "consent select_account"), // always re-issue the refresh token
		oauth2.S256ChallengeOption(verifier),
	}
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	authURL := cfg.AuthCodeURL(state, opts...)

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	var once sync.Once
	finish := func(r result) { once.Do(func() { done <- r }) }

	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		switch {
		case q.Get("state") != state:
			page(w, http.StatusBadRequest, "Invalid state — please retry from the terminal.")
			finish(result{err: errors.New("OAuth state mismatch")})
		case q.Get("error") != "":
			page(w, http.StatusBadRequest, "Authorization failed: "+q.Get("error"))
			finish(result{err: fmt.Errorf("authorization denied: %s", q.Get("error"))})
		case q.Get("code") == "":
			page(w, http.StatusBadRequest, "Missing authorization code.")
			finish(result{err: errors.New("callback without code")})
		default:
			page(w, http.StatusOK, "🐦 pigeon is authorized. You can close this tab and return to the terminal.")
			finish(result{code: q.Get("code")})
		}
	})}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	logf("Opening your browser to authorize Gmail access…\nIf it does not open, visit:\n\n  %s\n\n", authURL)
	_ = openBrowser(authURL)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return "", nil, fmt.Errorf("waiting for browser authorization: %w", ctx.Err())
	}
	if res.err != nil {
		return "", nil, res.err
	}

	tok, err := cfg.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", nil, fmt.Errorf("exchange code: %w", err)
	}
	if tok.RefreshToken == "" {
		return "", nil, errors.New("Google returned no refresh token; revoke pigeon at https://myaccount.google.com/permissions and retry")
	}
	if !grantedAll(tok) {
		return "", nil, errors.New("not all permissions were granted; retry and tick every checkbox on the consent screen")
	}

	email, err := ProfileEmail(ctx, cfg.Client(ctx, tok))
	if err != nil {
		return "", nil, err
	}
	return email, tok, nil
}

// grantedAll checks Google's granular consent: users can untick scopes.
func grantedAll(tok *oauth2.Token) bool {
	granted, _ := tok.Extra("scope").(string)
	if granted == "" {
		return true // not reported; the first API call will tell
	}
	have := strings.Fields(granted)
	for _, s := range Scopes {
		found := false
		for _, h := range have {
			if h == s || h == "https://mail.google.com/" {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ProfileEmail asks Gmail which address the token belongs to.
func ProfileEmail(ctx context.Context, c *http.Client) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://gmail.googleapis.com/gmail/v1/users/me/profile", nil)
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("gmail profile: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return "", fmt.Errorf("gmail profile: %s %s", resp.Status, e.Error.Message)
	}
	var p struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return "", err
	}
	return p.EmailAddress, nil
}

// --- token storage (OS keychain) ---

// SaveToken stores the token in the keychain under the account email.
func SaveToken(email string, tok *oauth2.Token) error {
	data, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return keyring.Set(keyringService, strings.ToLower(email), string(data))
}

func loadToken(email string) (*oauth2.Token, error) {
	s, err := keyring.Get(keyringService, strings.ToLower(email))
	if err != nil {
		return nil, fmt.Errorf("no token for %s in keychain (re-run `pigeon account add`): %w", email, err)
	}
	var tok oauth2.Token
	return &tok, json.Unmarshal([]byte(s), &tok)
}

// DeleteToken revokes the token at Google (best effort) and removes it from the keychain.
func DeleteToken(ctx context.Context, email string) error {
	if tok, err := loadToken(email); err == nil {
		t := tok.RefreshToken
		if t == "" {
			t = tok.AccessToken
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/revoke",
			strings.NewReader(url.Values{"token": {t}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}
	err := keyring.Delete(keyringService, strings.ToLower(email))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Client returns an authorized HTTP client for the account. Access tokens are
// refreshed automatically, and refreshed tokens are written back to the keychain.
func Client(ctx context.Context, email string) (*http.Client, error) {
	cfg, err := oauthConfig()
	if err != nil {
		return nil, err
	}
	tok, err := loadToken(email)
	if err != nil {
		return nil, err
	}
	src := &persistingSource{email: email, last: tok, base: oauth2.ReuseTokenSource(tok, cfg.TokenSource(ctx, tok))}
	return oauth2.NewClient(ctx, src), nil
}

type persistingSource struct {
	email string
	base  oauth2.TokenSource
	mu    sync.Mutex
	last  *oauth2.Token
}

func (s *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok.AccessToken != s.last.AccessToken {
		s.last = tok
		_ = SaveToken(s.email, tok) // a failed save only costs an extra refresh next run
	}
	return tok, nil
}

// --- helpers ---

func randomString() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func page(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>pigeon</title>
<body style="font-family:ui-monospace,monospace;background:#1e1f2b;color:#c8d0f0;display:grid;place-items:center;height:100vh;margin:0">
<p>%s</p>`, html.EscapeString(msg))
}
