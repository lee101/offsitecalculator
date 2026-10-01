package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeAppnz stands in for app.nz: token exchange, balance, and a chat endpoint
// that calls one tool and then answers.
func fakeAppnz(t *testing.T, chatStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "good-code" || body["code_verifier"] == "" || body["client_id"] != "cid" {
			http.Error(w, `{"error":"bad"}`, 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600, "scope": "credits:read models:chat profile",
			"models": []string{"gpt-5.5"}, "user": map[string]string{"id": "u1", "email": "a@b.c", "handle": "abc"}})
	})
	mux.HandleFunc("GET /api/credits/balance", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"balance": map[string]int{"total": 4242}})
	})
	calls := 0
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if chatStatus != 200 {
			http.Error(w, `{"error":"x"}`, chatStatus)
			return
		}
		calls++
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "gpt-5.5" {
			t.Errorf("model = %q", req.Model)
		}
		if calls == 1 {
			json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
				"id": "c1", "type": "function", "function": map[string]string{"name": "find_offsite_destinations", "arguments": `{"max_stops":1}`}}}}}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "Meet in Sydney."}}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestServer(t *testing.T, appnzURL string) *Server {
	t.Helper()
	s, err := New(Config{BaseURL: "http://offsite.test", AppnzBaseURL: appnzURL, ClientID: "cid", SessionSecret: "0123456789abcdef0123", Model: "gpt-5.5"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func signIn(t *testing.T, s *Server) []*http.Cookie {
	t.Helper()
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/login", nil))
	if rec.Code != 302 {
		t.Fatalf("login status %d %s", rec.Code, rec.Body)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q := loc.Query()
	if q.Get("models") != "gpt-5.5" || !strings.Contains(q.Get("scope"), "models:chat") || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "http://offsite.test/api/auth/callback" {
		t.Fatalf("bad authorize url %s", loc)
	}
	req := httptest.NewRequest("GET", "/api/auth/callback?code=good-code&state="+q.Get("state"), nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	cb := httptest.NewRecorder()
	h.ServeHTTP(cb, req)
	if cb.Code != 302 {
		t.Fatalf("callback status %d %s", cb.Code, cb.Body)
	}
	for _, c := range cb.Result().Cookies() {
		if c.Name == sessionCookie {
			if !c.HttpOnly {
				t.Fatal("session cookie must be HttpOnly")
			}
			return []*http.Cookie{c}
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func do(t *testing.T, h http.Handler, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLoginAgentFlow(t *testing.T) {
	s := newTestServer(t, fakeAppnz(t, 200).URL)
	cookies := signIn(t, s)
	h := s.Handler()

	me := do(t, h, "GET", "/api/me", "", cookies)
	if !strings.Contains(me.Body.String(), `"credits":4242`) || !strings.Contains(me.Body.String(), `"agentReady":true`) {
		t.Fatalf("me = %s", me.Body)
	}
	body := `{"messages":[{"role":"user","content":"where?"}],"people":[{"name":"A","lat":-36.85,"lng":174.76},{"name":"B","lat":-33.87,"lng":151.21}]}`
	res := do(t, h, "POST", "/api/agent", body, cookies)
	if res.Code != 200 {
		t.Fatalf("agent %d %s", res.Code, res.Body)
	}
	var out struct {
		Reply string `json:"reply"`
		Tools []struct{ Name string }
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Reply != "Meet in Sydney." || len(out.Tools) != 1 || out.Tools[0].Name != "find_offsite_destinations" {
		t.Fatalf("agent result %s", res.Body)
	}
}

func TestAgentRequiresSignIn(t *testing.T) {
	s := newTestServer(t, fakeAppnz(t, 200).URL)
	rec := do(t, s.Handler(), "POST", "/api/agent", `{"messages":[{"role":"user","content":"x"}]}`, nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "signin_required") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAgentOutOfCreditsPointsToTopUp(t *testing.T) {
	s := newTestServer(t, fakeAppnz(t, 402).URL)
	cookies := signIn(t, s)
	rec := do(t, s.Handler(), "POST", "/api/agent", `{"messages":[{"role":"user","content":"x"}]}`, cookies)
	if rec.Code != 402 || !strings.Contains(rec.Body.String(), "insufficient_credits") || !strings.Contains(rec.Body.String(), "/billing") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestCallbackRejectsWrongState(t *testing.T) {
	s := newTestServer(t, fakeAppnz(t, 200).URL)
	h := s.Handler()
	rec := do(t, h, "GET", "/api/auth/login", "", nil)
	req := httptest.NewRequest("GET", "/api/auth/callback?code=good-code&state=forged", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	cb := httptest.NewRecorder()
	h.ServeHTTP(cb, req)
	if cb.Code != 400 {
		t.Fatalf("forged state accepted: %d", cb.Code)
	}
}

func TestSolveAndGeocodePublic(t *testing.T) {
	h := newTestServer(t, "http://unused").Handler()
	rec := do(t, h, "POST", "/api/geocode", `{"text":"Alice: Auckland\nBob: Sydney"}`, nil)
	if rec.Code != 200 || strings.Count(rec.Body.String(), `"resolved":true`) != 2 {
		t.Fatalf("geocode %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "POST", "/api/solve", `{"people":[{"name":"A","lat":-36.85,"lng":174.76},{"name":"B","lat":-33.87,"lng":151.21}]}`, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"options"`) {
		t.Fatalf("solve %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/api/solve", `{}`, nil); rec.Code != 400 {
		t.Fatalf("empty team should be 400, got %d", rec.Code)
	}
}
