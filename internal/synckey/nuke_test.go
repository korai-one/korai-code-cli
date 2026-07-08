package synckey_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/synchub"
	"github.com/Nevaero/korai-code-cli/internal/synckey"
)

// readFile is a small test helper that reads a whole file as a string.
func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	return string(b), err
}

// TestNukeVerifierSetAndVerify verifies the verifier accepts the right code and
// rejects a wrong one, and that a missing verifier verifies as false with no
// error (so the feature cannot be probed).
func TestNukeVerifierSetAndVerify(t *testing.T) {
	home := t.TempDir()

	// No verifier configured yet: a verify must be false, not an error.
	ok, err := synckey.VerifyNukeCode(home, "anything")
	if err != nil || ok {
		t.Fatalf("unset verifier: ok=%v err=%v", ok, err)
	}

	if err := synckey.SetNukeVerifier(home, "the-duress-code"); err != nil {
		t.Fatalf("SetNukeVerifier: %v", err)
	}
	if _, err := os.Stat(synckey.NukeVerifierPath(home)); err != nil {
		t.Fatalf("verifier file missing: %v", err)
	}

	ok, err = synckey.VerifyNukeCode(home, "the-duress-code")
	if err != nil || !ok {
		t.Fatalf("correct code: ok=%v err=%v", ok, err)
	}
	ok, err = synckey.VerifyNukeCode(home, "wrong-code")
	if err != nil {
		t.Fatalf("wrong code errored: %v", err)
	}
	if ok {
		t.Error("wrong code was accepted")
	}
	if err := synckey.SetNukeVerifier(home, "  "); err == nil {
		t.Error("expected an error setting an empty nuke code")
	}
}

// TestWipeShredsPurgesAndCallsRemote drives the full duress wipe against temp
// dirs and a mock hub: it asserts the key is zeroized, every local trace is
// gone, and the hub DELETE was issued.
func TestWipeShredsPurgesAndCallsRemote(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	// Seed the full purge surface.
	korai := filepath.Join(home, ".korai")
	mustMkdir(t, korai)
	mustMkdir(t, filepath.Join(korai, "sessions"))
	mustMkdir(t, filepath.Join(korai, "snapshots"))
	mustMkdir(t, filepath.Join(project, ".korai"))

	key := bytes.Repeat([]byte{0x7f}, synckey.KeyLen)
	if err := synckey.Save(home, key); err != nil {
		t.Fatal(err)
	}
	recovery, err := synckey.WrapRecovery(key, "recover-pass")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, synckey.RecoveryPath(home), recovery)
	mustWrite(t, filepath.Join(korai, "sessions.db"), "db")
	mustWrite(t, filepath.Join(korai, "sessions", "abc.jsonl"), "{}")
	mustWrite(t, filepath.Join(korai, "snapshots", "snap.txt"), "snap")
	mustWrite(t, filepath.Join(korai, "sync-cursor"), "42")
	mustWrite(t, filepath.Join(project, ".korai", "MEMORY.md"), "secret memory")

	// Mock hub that records a DELETE /v1/sync with the derived bearer.
	var deleteCalled bool
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sync" {
			deleteCalled = true
			gotAuth = r.Header.Get("Authorization")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Capture the expected bearer before Wipe zeroizes the key.
	wantBearer := "Bearer " + synckey.DeriveSyncID(key)
	client := synchub.NewClient(srv.URL, synckey.DeriveSyncID(key), srv.Client())
	report := synckey.Wipe(context.Background(), key, synckey.DefaultWipePaths(home, project), client.WipeRemote)

	// Key zeroized in memory.
	if !report.KeyShredded {
		t.Error("report.KeyShredded is false")
	}
	if !bytes.Equal(key, make([]byte, synckey.KeyLen)) {
		t.Error("key was not zeroized in memory")
	}

	// Every seeded path is gone.
	for _, p := range []string{
		synckey.KeyPath(home),
		synckey.RecoveryPath(home),
		filepath.Join(korai, "sessions.db"),
		filepath.Join(korai, "sessions"),
		filepath.Join(korai, "snapshots"),
		filepath.Join(korai, "sync-cursor"),
		filepath.Join(project, ".korai", "MEMORY.md"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s gone, stat err = %v", p, err)
		}
	}

	// Remote purge happened with the derived bearer.
	if !deleteCalled {
		t.Error("hub DELETE /v1/sync was not called")
	}
	if gotAuth != wantBearer {
		t.Errorf("bearer header = %q, want %q", gotAuth, wantBearer)
	}
	if !report.RemoteWiped {
		t.Error("report.RemoteWiped is false despite a 200 from the hub")
	}
	if len(report.Errs) != 0 {
		t.Errorf("unexpected wipe errors: %v", report.Errs)
	}
}

// TestWipeIdempotent verifies a second wipe over already-absent targets is safe
// and reports nothing removed and no errors (nil key, nil remote).
func TestWipeIdempotent(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	report := synckey.Wipe(context.Background(), nil, synckey.DefaultWipePaths(home, project), nil)
	if len(report.Removed) != 0 {
		t.Errorf("expected nothing removed, got %v", report.Removed)
	}
	if len(report.Errs) != 0 {
		t.Errorf("expected no errors, got %v", report.Errs)
	}
	if report.RemoteWiped {
		t.Error("RemoteWiped should be false with a nil remotePurge")
	}
}

// TestWipeContinuesWhenRemoteFails verifies a hub failure is recorded but does
// not block the crypto-shred/local purge (best-effort remote).
func TestWipeContinuesWhenRemoteFails(t *testing.T) {
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".korai"))
	key := bytes.Repeat([]byte{0x11}, synckey.KeyLen)
	if err := synckey.Save(home, key); err != nil {
		t.Fatal(err)
	}
	failing := func(context.Context) error { return errRemote }
	report := synckey.Wipe(context.Background(), key, synckey.DefaultWipePaths(home, ""), failing)

	if !report.KeyShredded {
		t.Error("shred should still happen when remote fails")
	}
	if _, err := os.Stat(synckey.KeyPath(home)); !os.IsNotExist(err) {
		t.Error("key file should be gone even when remote fails")
	}
	if report.RemoteWiped {
		t.Error("RemoteWiped should be false on remote failure")
	}
	if len(report.Errs) == 0 {
		t.Error("expected the remote failure recorded in Errs")
	}
}

var errRemote = errors.New("remote unavailable")

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
