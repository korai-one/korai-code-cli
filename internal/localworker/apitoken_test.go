package localworker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeGatedAdvert writes a local-worker.json advertising url plus the bearer
// its /v1/* requires — what a worker publishes once it binds off loopback.
func writeGatedAdvert(t *testing.T, home, url, apiToken string) {
	t.Helper()
	korDir := filepath.Join(home, ".korai")
	if err := os.MkdirAll(korDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Info{URL: url, PID: 1234, APIToken: apiToken})
	if err := os.WriteFile(filepath.Join(korDir, "local-worker.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// gatedHealthServer answers /health only when the right bearer is presented,
// and records what it saw. Stricter than a real worker (whose /health is
// ungated) on purpose: it proves the probe actually sends the header rather
// than passing because the endpoint was open anyway.
func gatedHealthServer(t *testing.T, want string, sawAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*sawAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The advert's token must reach the resolved endpoint. Without it the caller
// builds an unauthenticated client and every completion 401s — silently, since
// the failure only shows up once the session is already running.
func TestResolveCarriesAdvertisedAPIToken(t *testing.T) {
	home := setHome(t)
	var sawAuth string
	srv := gatedHealthServer(t, "sekret", &sawAuth)
	writeGatedAdvert(t, home, srv.URL, "sekret")

	ep, ok := Resolve(context.Background(), "", "", "", srv.Client())
	if !ok {
		t.Fatal("Resolve returned ok=false for a healthy gated worker")
	}
	if ep.IsDirect() {
		t.Error("expected an HTTP endpoint, got a direct channel")
	}
	if ep.Token != "sekret" {
		t.Errorf("Token = %q, want the advertised apiToken", ep.Token)
	}
	if sawAuth != "Bearer sekret" {
		t.Errorf("probe sent Authorization %q, want %q", sawAuth, "Bearer sekret")
	}
}

// A gated worker whose token we do NOT have must fail the probe and fall back
// to the network, rather than resolving to an endpoint that cannot be used.
func TestResolveRejectsGatedWorkerWithoutToken(t *testing.T) {
	home := setHome(t)
	var sawAuth string
	srv := gatedHealthServer(t, "sekret", &sawAuth)
	// Advert with a url but NO apiToken — e.g. hand-written, or truncated.
	writeGatedAdvert(t, home, srv.URL, "")

	if _, ok := Resolve(context.Background(), "", "", "", srv.Client()); ok {
		t.Fatal("resolved a gated worker we cannot authenticate to")
	}
}

// An explicit --local-worker-url is how a sandboxed session reaches the worker
// through host.docker.internal, and it needs the operator's token to travel
// with it. It is used without a probe, so the token is the only thing that
// makes the resulting client work.
func TestResolveExplicitURLCarriesToken(t *testing.T) {
	setHome(t)
	ep, ok := Resolve(
		context.Background(),
		"http://host.docker.internal:63535/",
		"",
		"sekret",
		nil,
	)
	if !ok {
		t.Fatal("Resolve returned ok=false for an explicit URL")
	}
	if ep.URL != "http://host.docker.internal:63535" {
		t.Errorf("url = %q, want the trailing slash trimmed", ep.URL)
	}
	if ep.Token != "sekret" {
		t.Errorf("Token = %q, want the caller's token", ep.Token)
	}
}

// An ungated worker (the loopback default) must keep resolving with no token,
// so the common case is untouched by any of this.
func TestResolveUngatedWorkerNeedsNoToken(t *testing.T) {
	home := setHome(t)
	srv := healthServer(t, true)
	writeAdvert(t, home, srv.URL)

	ep, ok := Resolve(context.Background(), "", "", "", srv.Client())
	if !ok {
		t.Fatal("Resolve returned ok=false for a healthy ungated worker")
	}
	if ep.Token != "" {
		t.Errorf("Token = %q, want empty for an ungated worker", ep.Token)
	}
}

// The advert field name is a cross-repo contract with cmd/worker/advert.go.
func TestInfoParsesApiTokenField(t *testing.T) {
	var info Info
	if err := json.Unmarshal([]byte(`{"url":"http://x:1","token":"control","apiToken":"inference"}`), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.APIToken != "inference" {
		t.Errorf("APIToken = %q, want %q", info.APIToken, "inference")
	}
}
