package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestLoginBrowserLoopback drives the whole Authorization Code + PKCE loopback
// flow with a fake browser: the injected opener parses the authorize URL and
// redirects to the loopback callback with a code, and a fake orchestrator
// exchanges it. It asserts the callback's state is echoed and the PKCE verifier
// is presented at the token endpoint.
func TestLoginBrowserLoopback(t *testing.T) {
	var gotVerifier, gotCode string
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		gotVerifier = r.Form.Get("code_verifier")
		gotCode = r.Form.Get("code")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc.jwt","refresh_token":"krr_s.sec","token_type":"Bearer","expires_in":900}`))
	}))
	defer orch.Close()

	// The fake browser: extract redirect_uri + state from the authorize URL and
	// hit the loopback listener with a code, exactly as the real web page would.
	open := func(rawURL string) error {
		u, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		q := u.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			t.Errorf("authorize URL missing S256 challenge: %s", rawURL)
		}
		cb := q.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(q.Get("state"))
		go func() {
			resp, herr := http.Get(cb) //nolint:noctx // test-local loopback call
			if herr == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := NewClient(orch.URL).LoginBrowser(ctx, "https://web.example", "korai-cli @ test", open, func(string) {})
	if err != nil {
		t.Fatalf("LoginBrowser: %v", err)
	}
	if tok.AccessToken != "acc.jwt" || tok.RefreshToken != "krr_s.sec" {
		t.Errorf("token not stored from exchange: %+v", tok)
	}
	if tok.BaseURL != orch.URL {
		t.Errorf("BaseURL = %q, want %q", tok.BaseURL, orch.URL)
	}
	if gotCode != "the-code" {
		t.Errorf("orchestrator saw code %q", gotCode)
	}
	if gotVerifier == "" {
		t.Error("PKCE verifier not presented at token endpoint")
	}
}

// TestLoginBrowserStateMismatch ensures a callback with the wrong state is
// rejected (CSRF guard) rather than exchanged.
func TestLoginBrowserStateMismatch(t *testing.T) {
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("token endpoint must not be called on a state mismatch")
		w.WriteHeader(http.StatusOK)
	}))
	defer orch.Close()

	open := func(rawURL string) error {
		u, _ := url.Parse(rawURL)
		cb := u.Query().Get("redirect_uri") + "?code=x&state=WRONG"
		go func() {
			resp, herr := http.Get(cb) //nolint:noctx // test-local loopback call
			if herr == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := NewClient(orch.URL).LoginBrowser(ctx, "https://web.example", "d", open, func(string) {}); err == nil {
		t.Fatal("expected an error on state mismatch")
	}
}
