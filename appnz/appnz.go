// Package appnz is a small Go client for building agents on app.nz: sign in
// with an app.nz account (OAuth + PKCE), run an approved model against the
// person's own credits, and drive a tool-calling agent loop.
//
// It has no dependencies and keeps no server-side state, so it fits a
// scale-to-zero container: the session lives in a sealed cookie.
package appnz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://app.nz"

const (
	ScopeProfile      = "profile"
	ScopeCreditsRead  = "credits:read"
	ScopeModelsChat   = "models:chat"
	ScopeCreditsSpend = "credits:spend"
)

var (
	ErrInsufficientCredits = errors.New("insufficient app.nz credits")
	ErrModelNotApproved    = errors.New("model not approved for this sign-in")
	ErrUnauthorized        = errors.New("app.nz sign-in expired")
)

type Client struct {
	BaseURL  string
	ClientID string
	HTTP     *http.Client
}

func New(clientID string) *Client {
	return &Client{BaseURL: DefaultBaseURL, ClientID: clientID, HTTP: &http.Client{Timeout: 150 * time.Second}}
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

type AuthRequest struct {
	URL      string `json:"url"`
	State    string `json:"state"`
	Verifier string `json:"verifier"`
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// AuthStart builds the app.nz consent URL. models pins models:chat to the
// models the person will see and approve; app.nz refuses every other model.
func (c *Client) AuthStart(redirectURI string, scopes, models []string) AuthRequest {
	verifier := random(48)
	sum := sha256.Sum256([]byte(verifier))
	state := random(18)
	q := url.Values{
		"client_id":             {c.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	if len(models) > 0 {
		q.Set("models", strings.Join(models, " "))
	}
	return AuthRequest{URL: c.base() + "/authorize?" + q.Encode(), State: state, Verifier: verifier}
}

type User struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Handle string `json:"handle"`
}

type Token struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	Scope       string    `json:"scope"`
	Models      []string  `json:"models,omitempty"`
	User        User      `json:"user"`
}

func (t *Token) Valid() bool {
	return t != nil && t.AccessToken != "" && time.Now().Before(t.ExpiresAt.Add(-30*time.Second))
}

func (t *Token) HasScope(scope string) bool {
	for _, s := range strings.Fields(t.Scope) {
		if s == scope {
			return true
		}
	}
	return false
}

func (c *Client) Exchange(ctx context.Context, redirectURI, code, verifier string) (*Token, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type": "authorization_code", "code": code, "redirect_uri": redirectURI,
		"client_id": c.ClientID, "code_verifier": verifier,
	})
	var out struct {
		AccessToken string   `json:"access_token"`
		ExpiresIn   int      `json:"expires_in"`
		Scope       string   `json:"scope"`
		Models      []string `json:"models"`
		User        User     `json:"user"`
	}
	if err := c.do(ctx, "POST", "/token", "", body, &out); err != nil {
		return nil, err
	}
	return &Token{
		AccessToken: out.AccessToken, ExpiresAt: time.Now().Add(time.Duration(out.ExpiresIn) * time.Second),
		Scope: out.Scope, Models: out.Models, User: out.User,
	}, nil
}

func (c *Client) Revoke(ctx context.Context, token string) {
	body, _ := json.Marshal(map[string]string{"client_id": c.ClientID, "token": token})
	_ = c.do(ctx, "POST", "/revoke", "", body, nil)
}

type Balance struct {
	Free  int `json:"free"`
	Plan  int `json:"plan"`
	Paid  int `json:"paid"`
	Total int `json:"total"`
}

// Balance needs the credits:read scope.
func (c *Client) Balance(ctx context.Context, token string) (*Balance, error) {
	var out struct {
		Balance Balance `json:"balance"`
	}
	if err := c.do(ctx, "GET", "/api/credits/balance", token, nil, &out); err != nil {
		return nil, err
	}
	return &out.Balance, nil
}

// TopUpURL is where a person adds credits; the app never touches payment details.
func (c *Client) TopUpURL() string { return c.base() + "/billing" }

type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("app.nz %d: %s", e.Status, e.Body) }

func (c *Client) do(ctx context.Context, method, path, token string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == 401:
		return ErrUnauthorized
	case resp.StatusCode == 402:
		return ErrInsufficientCredits
	case resp.StatusCode == 403 && strings.Contains(string(data), "model not approved"):
		return ErrModelNotApproved
	case resp.StatusCode >= 300:
		return &apiError{resp.StatusCode, strings.TrimSpace(string(data))}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
