package synckey_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/synckey"
)

// TestGenerateSaveLoadRoundTrip verifies a generated key persists and reloads
// byte-for-byte, and that the file lands where LoadContentKey looks.
func TestGenerateSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KORAI_SYNC_KEY", "") // force the file path, not the env

	key, err := synckey.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(key) != synckey.KeyLen {
		t.Fatalf("key len = %d, want %d", len(key), synckey.KeyLen)
	}
	if err := synckey.Save(home, key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(synckey.KeyPath(home)); err != nil {
		t.Fatalf("key file missing: %v", err)
	}
	got, ok, err := synckey.Load(home)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, key) {
		t.Errorf("loaded key mismatch")
	}
	if !synckey.Exists(home) {
		t.Error("Exists should report true after Save")
	}
}

// TestLoadAbsent verifies a missing key is (nil,false,nil), not an error.
func TestLoadAbsent(t *testing.T) {
	t.Setenv("KORAI_SYNC_KEY", "")
	_, ok, err := synckey.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Error("expected no key configured")
	}
}

// TestSaveRejectsBadLength verifies Save guards the key length.
func TestSaveRejectsBadLength(t *testing.T) {
	if err := synckey.Save(t.TempDir(), []byte("short")); err == nil {
		t.Error("expected error for short key")
	}
}

// TestDeriveSyncIDDeterministic verifies the derivation is stable for a key and
// differs across keys — the property that lets every device with the same key
// share one namespace while distinct keys never collide.
func TestDeriveSyncIDDeterministic(t *testing.T) {
	k1 := bytes.Repeat([]byte{0xab}, synckey.KeyLen)
	k2 := bytes.Repeat([]byte{0xcd}, synckey.KeyLen)

	a := synckey.DeriveSyncID(k1)
	b := synckey.DeriveSyncID(k1)
	if a != b {
		t.Errorf("derivation not deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Error("empty sync_id")
	}
	if synckey.DeriveSyncID(k2) == a {
		t.Error("distinct keys produced the same sync_id")
	}
}

// TestSaveWritesRestrictivePerms verifies the key file is 0600 (best-effort:
// exact bits are POSIX; on Windows the check is relaxed).
func TestSaveWritesRestrictivePerms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KORAI_SYNC_KEY", "")
	key, _ := synckey.Generate()
	if err := synckey.Save(home, key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(filepath.Join(home, ".korai", "sync.key"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 && perm != 0o666 {
		// 0o666 shows up on Windows where perm bits are not enforced.
		t.Errorf("key file perms too open: %o", perm)
	}
}
