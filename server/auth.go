package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/lee101/offsitecalculator/appnz"
)

const (
	sessionCookie = "oc_session"
	flowCookie    = "oc_flow"
)

type flow struct {
	State    string    `json:"s"`
	Verifier string    `json:"v"`
	Expires  time.Time `json:"e"`
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true,
		Secure: len(s.origin(r)) > 5 && s.origin(r)[:5] == "https", SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) session(r *http.Request) *appnz.Token {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	var tok appnz.Token
	if s.sealer.Open(c.Value, &tok) != nil || !tok.Valid() {
		return nil
	}
	return &tok
}

func (s *Server) redirectURI(r *http.Request) string { return s.origin(r) + "/api/auth/callback" }

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ClientID == "" {
		writeErr(w, 503, "APPNZ_CLIENT_ID is not configured")
		return
	}
	auth := s.app.AuthStart(s.redirectURI(r),
		[]string{appnz.ScopeProfile, appnz.ScopeCreditsRead, appnz.ScopeModelsChat}, []string{s.cfg.Model})
	sealed, err := s.sealer.Seal(flow{State: auth.State, Verifier: auth.Verifier, Expires: time.Now().Add(10 * time.Minute)})
	if err != nil {
		writeErr(w, 500, "could not start sign-in")
		return
	}
	s.setCookie(w, r, flowCookie, sealed, 600)
	http.Redirect(w, r, auth.URL, http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Redirect(w, r, "/?auth="+e, http.StatusFound)
		return
	}
	c, err := r.Cookie(flowCookie)
	var f flow
	if err != nil || s.sealer.Open(c.Value, &f) != nil || time.Now().After(f.Expires) || f.State == "" || f.State != q.Get("state") {
		writeErr(w, 400, "sign-in expired, please try again")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := s.app.Exchange(ctx, s.redirectURI(r), q.Get("code"), f.Verifier)
	if err != nil {
		writeErr(w, 502, "app.nz sign-in failed")
		return
	}
	sealed, err := s.sealer.Seal(tok)
	if err != nil {
		writeErr(w, 500, "could not save session")
		return
	}
	s.setCookie(w, r, flowCookie, "", -1)
	s.setCookie(w, r, sessionCookie, sealed, int(time.Until(tok.ExpiresAt).Seconds()))
	http.Redirect(w, r, "/?auth=ok", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := s.session(r); tok != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		s.app.Revoke(ctx, tok.AccessToken)
	}
	s.setCookie(w, r, sessionCookie, "", -1)
	writeJSON(w, 200, map[string]any{"signedIn": false})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	tok := s.session(r)
	if tok == nil {
		writeJSON(w, 200, map[string]any{"signedIn": false})
		return
	}
	out := map[string]any{
		"signedIn": true, "user": tok.User, "models": tok.Models,
		"agentReady": tok.HasScope(appnz.ScopeModelsChat), "topUpUrl": s.app.TopUpURL(),
	}
	if tok.HasScope(appnz.ScopeCreditsRead) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		if bal, err := s.app.Balance(ctx, tok.AccessToken); err == nil {
			out["credits"] = bal.Total
		} else if errors.Is(err, appnz.ErrUnauthorized) {
			writeJSON(w, 200, map[string]any{"signedIn": false})
			return
		}
	}
	writeJSON(w, 200, out)
}
