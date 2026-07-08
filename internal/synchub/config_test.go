package synchub_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/synchub"
)

const hexKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// TestResolveDisabledByDefault verifies sync is off with no config and no env.
func TestResolveDisabledByDefault(t *testing.T) {
	t.Setenv("KORAI_SYNC_ENABLED", "")
	t.Setenv("KORAI_SYNC_KEY", "")
	cfg, err := synchub.Resolve(t.TempDir(), synchub.FileSettings{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.Enabled {
		t.Error("expected disabled by default")
	}
}

// TestResolveFromEnv verifies a fully env-configured sync resolves enabled with
// the right fields and default interval.
func TestResolveFromEnv(t *testing.T) {
	t.Setenv("KORAI_SYNC_ENABLED", "true")
	t.Setenv("KORAI_SYNC_URL", "https://hub.example/")
	t.Setenv("KORAI_SYNC_ID", "abc123")
	t.Setenv("KORAI_SYNC_KEY", hexKey)
	t.Setenv("KORAI_SYNC_INTERVAL", "")

	home := t.TempDir()
	cfg, err := synchub.Resolve(home, synchub.FileSettings{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("expected enabled")
	}
	if cfg.URL != "https://hub.example" { // trailing slash trimmed
		t.Errorf("URL = %q", cfg.URL)
	}
	if cfg.SyncID != "abc123" {
		t.Errorf("SyncID = %q", cfg.SyncID)
	}
	if len(cfg.Key) != 32 {
		t.Errorf("key len = %d", len(cfg.Key))
	}
	if cfg.Interval != 30*time.Second {
		t.Errorf("interval = %v, want default 30s", cfg.Interval)
	}
	if cfg.CursorPath != filepath.Join(home, ".korai", "sync-cursor") {
		t.Errorf("cursor path = %q", cfg.CursorPath)
	}
}

// TestResolveEnvOverridesSettings verifies env wins over the settings block.
func TestResolveEnvOverridesSettings(t *testing.T) {
	t.Setenv("KORAI_SYNC_ENABLED", "")
	t.Setenv("KORAI_SYNC_URL", "https://env-host")
	t.Setenv("KORAI_SYNC_ID", "")
	t.Setenv("KORAI_SYNC_KEY", hexKey)
	t.Setenv("KORAI_SYNC_INTERVAL", "45s")

	fs := synchub.FileSettings{Enabled: true, URL: "https://file-host", SyncID: "sid", Interval: "10s"}
	cfg, err := synchub.Resolve(t.TempDir(), fs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("settings enabled should switch sync on")
	}
	if cfg.URL != "https://env-host" {
		t.Errorf("env URL should win, got %q", cfg.URL)
	}
	if cfg.SyncID != "sid" {
		t.Errorf("settings SyncID should fill in, got %q", cfg.SyncID)
	}
	if cfg.Interval != 45*time.Second {
		t.Errorf("env interval should win, got %v", cfg.Interval)
	}
}

// TestResolveIncomplete verifies enabling without a key yields a disabled config
// and ErrIncomplete.
func TestResolveIncomplete(t *testing.T) {
	t.Setenv("KORAI_SYNC_ENABLED", "true")
	t.Setenv("KORAI_SYNC_URL", "https://hub")
	t.Setenv("KORAI_SYNC_ID", "sid")
	t.Setenv("KORAI_SYNC_KEY", "")

	cfg, err := synchub.Resolve(t.TempDir(), synchub.FileSettings{})
	if !errors.Is(err, synchub.ErrIncomplete) {
		t.Fatalf("expected ErrIncomplete, got %v", err)
	}
	if cfg.Enabled {
		t.Error("expected disabled config on incomplete setup")
	}
}

// TestResolveIntervalClamp verifies a sub-minimum interval is clamped up.
func TestResolveIntervalClamp(t *testing.T) {
	t.Setenv("KORAI_SYNC_ENABLED", "true")
	t.Setenv("KORAI_SYNC_URL", "https://hub")
	t.Setenv("KORAI_SYNC_ID", "sid")
	t.Setenv("KORAI_SYNC_KEY", hexKey)
	t.Setenv("KORAI_SYNC_INTERVAL", "1s")

	cfg, err := synchub.Resolve(t.TempDir(), synchub.FileSettings{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.Interval < 5*time.Second {
		t.Errorf("interval = %v, want clamped to >= 5s", cfg.Interval)
	}
}

// TestResolveKeyFromFile verifies the key file is used when env is unset.
func TestResolveKeyFromFile(t *testing.T) {
	t.Setenv("KORAI_SYNC_ENABLED", "true")
	t.Setenv("KORAI_SYNC_URL", "https://hub")
	t.Setenv("KORAI_SYNC_ID", "sid")
	t.Setenv("KORAI_SYNC_KEY", "")

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".korai"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".korai", "sync.key"), []byte(hexKey), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := synchub.Resolve(home, synchub.FileSettings{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !cfg.Enabled || len(cfg.Key) != 32 {
		t.Errorf("expected enabled with 32-byte key, got enabled=%v keylen=%d", cfg.Enabled, len(cfg.Key))
	}
}
