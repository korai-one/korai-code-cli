package localworker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/localproto"
)

// setHome points os.UserHomeDir at a temp dir for the duration of a test, on
// both Windows (USERPROFILE) and Unix (HOME), and returns the dir.
func setHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
	return dir
}

// writeAdvert writes a local-worker.json with the given URL under home.
func writeAdvert(t *testing.T, home, url string) {
	t.Helper()
	korDir := filepath.Join(home, ".korai")
	if err := os.MkdirAll(korDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Info{URL: url, PID: 1234, Models: []string{"gemma"}})
	if err := os.WriteFile(filepath.Join(korDir, "local-worker.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func healthServer(t *testing.T, ok bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestResolveAdvertisedHealthy: a fresh advert + a healthy worker resolves to
// the advertised URL.
func TestResolveAdvertisedHealthy(t *testing.T) {
	home := setHome(t)
	srv := healthServer(t, true)
	writeAdvert(t, home, srv.URL)

	ep, ok := Resolve(context.Background(), "", srv.Client())
	if !ok {
		t.Fatal("Resolve returned ok=false for a healthy advertised worker")
	}
	if ep.IsSocket() {
		t.Error("expected an HTTP endpoint, got a socket")
	}
	if ep.URL != srv.URL {
		t.Errorf("url = %q, want %q", ep.URL, srv.URL)
	}
}

// TestResolveAdvertisedUnhealthy: a stale advert whose worker fails the probe
// resolves to nothing, so the caller falls back to the network.
func TestResolveAdvertisedUnhealthy(t *testing.T) {
	home := setHome(t)
	srv := healthServer(t, false)
	writeAdvert(t, home, srv.URL)

	if _, ok := Resolve(context.Background(), "", srv.Client()); ok {
		t.Error("Resolve should reject an unhealthy worker")
	}
}

// TestResolveNoAdvert: no file means no local worker.
func TestResolveNoAdvert(t *testing.T) {
	setHome(t) // empty temp home, no advert file
	if _, ok := Resolve(context.Background(), "", nil); ok {
		t.Error("Resolve should be ok=false with no advert file")
	}
}

// TestResolveExplicitNoProbe: an explicit override is honored as-is, without a
// health probe — the operator asked for it. A trailing slash is trimmed.
func TestResolveExplicitNoProbe(t *testing.T) {
	setHome(t)
	ep, ok := Resolve(context.Background(), "http://127.0.0.1:9999/", nil)
	if !ok {
		t.Fatal("explicit override must resolve ok=true")
	}
	if ep.URL != "http://127.0.0.1:9999" {
		t.Errorf("url = %q, want trailing slash trimmed", ep.URL)
	}
}

// TestResolvePrefersSocket: when the advert carries a reachable socket, Resolve
// prefers it over the HTTP URL. Skips if the platform can't bind a unix socket.
func TestResolvePrefersSocket(t *testing.T) {
	home := setHome(t)
	sockPath := filepath.Join(home, "w.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix sockets unavailable here: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		ft, _, rerr := localproto.ReadFrame(conn)
		if rerr != nil || ft != localproto.FrameHello {
			return
		}
		_ = localproto.WriteJSON(conn, localproto.FrameReady, localproto.ReadyPayload{Version: localproto.ProtocolVersion})
	}()

	// Advert carries both a (bogus) URL and the live socket; the socket wins.
	korDir := filepath.Join(home, ".korai")
	if err := os.MkdirAll(korDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Info{URL: "http://127.0.0.1:1/", Socket: sockPath})
	if err := os.WriteFile(filepath.Join(korDir, "local-worker.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	ep, ok := Resolve(context.Background(), "", nil)
	if !ok || !ep.IsSocket() || ep.Socket != sockPath {
		t.Fatalf("expected socket endpoint %q, got %+v ok=%v", sockPath, ep, ok)
	}
}

// TestReadMalformed: a present but malformed advert reads as not-found.
func TestReadMalformed(t *testing.T) {
	home := setHome(t)
	korDir := filepath.Join(home, ".korai")
	_ = os.MkdirAll(korDir, 0o700)
	_ = os.WriteFile(filepath.Join(korDir, "local-worker.json"), []byte("{not json"), 0o600)

	if _, ok := Read(); ok {
		t.Error("malformed advert should read ok=false")
	}
}
