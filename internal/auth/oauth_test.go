package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestNextPollInterval(t *testing.T) {
	base := 5 * time.Second
	if got := nextPollInterval(base, ErrAuthorizationPending); got != base {
		t.Errorf("authorization_pending should not change interval: got %v", got)
	}
	if got := nextPollInterval(base, ErrSlowDown); got != base+slowDownStep {
		t.Errorf("slow_down should add %v: got %v want %v", slowDownStep, got, base+slowDownStep)
	}
	// slow_down compounds across repeated backoffs.
	step2 := nextPollInterval(nextPollInterval(base, ErrSlowDown), ErrSlowDown)
	if step2 != base+2*slowDownStep {
		t.Errorf("two slow_downs = %v, want %v", step2, base+2*slowDownStep)
	}
}

func TestParseErrorOAuthShape(t *testing.T) {
	err := parseError(http.StatusBadRequest, []byte(`{"error":"invalid_grant","error_description":"code used"}`))
	var oe *Error
	if !errors.As(err, &oe) {
		t.Fatalf("want *Error, got %T", err)
	}
	if oe.Code != ErrInvalidGrant || oe.Description != "code used" || oe.Status != 400 {
		t.Errorf("parsed wrong: %+v", oe)
	}
	if !IsInvalidGrant(err) {
		t.Error("IsInvalidGrant should report true for invalid_grant")
	}
}

func TestParseErrorFallback(t *testing.T) {
	err := parseError(http.StatusInternalServerError, []byte("gateway boom"))
	var oe *Error
	if !errors.As(err, &oe) {
		t.Fatalf("want *Error, got %T", err)
	}
	if oe.Code != "http_500" {
		t.Errorf("fallback code = %q, want http_500", oe.Code)
	}
	if IsInvalidGrant(err) {
		t.Error("a 500 is not invalid_grant")
	}
}

func TestRefreshRotates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "krr_old.secret" {
			t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new.jwt","refresh_token":"krr_new.secret","token_type":"Bearer","expires_in":900,"scope":"openid email"}`))
	}))
	defer srv.Close()

	tr, err := NewClient(srv.URL).Refresh(context.Background(), "krr_old.secret")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tr.AccessToken != "new.jwt" || tr.RefreshToken != "krr_new.secret" {
		t.Errorf("rotation not reflected: %+v", tr)
	}
}

func TestRefreshInvalidGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"family revoked"}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).Refresh(context.Background(), "krr_dead.secret")
	if !IsInvalidGrant(err) {
		t.Fatalf("want invalid_grant, got %v", err)
	}
}

func TestRevokeAlwaysOK(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotToken = r.Form.Get("token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).Revoke(context.Background(), "krr_x.y"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if gotToken != "krr_x.y" {
		t.Errorf("server saw token %q", gotToken)
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	got := buildAuthorizeURL("https://korai.one/", "http://127.0.0.1:5555/callback", "chal", "st8", "korai-cli @ host")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Host != "korai.one" || u.Path != "/authorize" {
		t.Errorf("wrong host/path: %s", got)
	}
	q := u.Query()
	checks := map[string]string{
		"client_id":             ClientID,
		"redirect_uri":          "http://127.0.0.1:5555/callback",
		"code_challenge":        "chal",
		"code_challenge_method": "S256",
		"state":                 "st8",
		"scope":                 DefaultScope,
		"device_label":          "korai-cli @ host",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
}
