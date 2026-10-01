package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lee101/offsitecalculator/appnz"
	"github.com/lee101/offsitecalculator/offsite"
)

type agentRequest struct {
	Messages    []appnz.Message     `json:"messages"`
	People      []offsite.Person    `json:"people"`
	Constraints offsite.Constraints `json:"constraints"`
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	tok := s.session(r)
	if tok == nil {
		writeJSON(w, 401, map[string]any{"error": errNotSignedIn.Error(), "code": "signin_required"})
		return
	}
	if !tok.HasScope(appnz.ScopeModelsChat) {
		writeJSON(w, 403, map[string]any{"error": "grant model access to use the agent", "code": "signin_required"})
		return
	}
	var req agentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Messages) == 0 || len(req.Messages) > 40 || len(req.People) > 100 {
		writeErr(w, 400, "bad request")
		return
	}
	history := make([]appnz.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if (m.Role == "user" || m.Role == "assistant") && len(m.Content) <= 4000 {
			history = append(history, appnz.Message{Role: m.Role, Content: m.Content})
		}
	}
	people := req.People
	agent := &appnz.Agent{
		Client: s.app, Model: s.cfg.Model, System: offsite.SystemPrompt, MaxSteps: 6,
		Tools: offsite.Tools(s.db, s.geo,
			func() []offsite.Person { return append([]offsite.Person(nil), people...) },
			func() offsite.Constraints { return req.Constraints }),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	res, err := agent.Run(ctx, tok.AccessToken, history)
	switch {
	case errors.Is(err, appnz.ErrInsufficientCredits):
		writeJSON(w, 402, map[string]any{"error": "Out of app.nz credits.", "code": "insufficient_credits", "topUpUrl": s.app.TopUpURL()})
	case errors.Is(err, appnz.ErrUnauthorized):
		writeJSON(w, 401, map[string]any{"error": "Your app.nz sign-in expired.", "code": "signin_required"})
	case errors.Is(err, appnz.ErrModelNotApproved):
		writeJSON(w, 403, map[string]any{"error": "Sign in again to approve " + s.cfg.Model + ".", "code": "signin_required"})
	case err != nil && res != nil && res.Reply == "":
		writeJSON(w, 502, map[string]any{"error": "The agent could not finish: " + err.Error(), "tools": res.Tools})
	default:
		b, _ := json.Marshal(res)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}
