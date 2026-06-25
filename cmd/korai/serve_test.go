package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleWSTokenGate verifies the --auth-token gate runs before the upgrade:
// a missing or wrong token is rejected with 401, and the correct token passes
// the gate (the subsequent upgrade then fails only because the test request
// carries no WebSocket headers — which is not a 401).
func TestHandleWSTokenGate(t *testing.T) {
	srv := &server{authToken: "s3cret", originPatterns: defaultOriginPatterns}

	cases := []struct {
		name       string
		target     string
		wantUnauth bool
	}{
		{"missing token", "/ws", true},
		{"wrong token", "/ws?token=nope", true},
		{"correct token", "/ws?token=s3cret", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			w := httptest.NewRecorder()
			srv.handleWS(w, req)
			gotUnauth := w.Code == http.StatusUnauthorized
			if gotUnauth != tc.wantUnauth {
				t.Errorf("status %d, wantUnauthorized=%v", w.Code, tc.wantUnauth)
			}
		})
	}
}

// TestHandleWSNoTokenConfigured verifies that with no auth token configured the
// gate is skipped entirely (a request without a token is not rejected with 401).
func TestHandleWSNoTokenConfigured(t *testing.T) {
	srv := &server{originPatterns: defaultOriginPatterns}
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	w := httptest.NewRecorder()
	srv.handleWS(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Error("no configured token should not gate the upgrade")
	}
}
