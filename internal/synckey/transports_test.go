package synckey_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/synckey"
)

// TestMnemonicRoundTrip verifies a key encodes to 24 words and decodes back
// exactly, tolerating extra whitespace.
func TestMnemonicRoundTrip(t *testing.T) {
	key, err := synckey.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	m, err := synckey.Mnemonic(key)
	if err != nil {
		t.Fatalf("Mnemonic: %v", err)
	}
	if n := len(strings.Fields(m)); n != 24 {
		t.Errorf("word count = %d, want 24", n)
	}
	got, err := synckey.KeyFromMnemonic("  " + strings.ReplaceAll(m, " ", "   ") + "\n")
	if err != nil {
		t.Fatalf("KeyFromMnemonic: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Error("mnemonic did not round-trip")
	}
}

// TestMnemonicRejectsBad verifies a corrupt mnemonic (bad checksum) is rejected.
func TestMnemonicRejectsBad(t *testing.T) {
	key, _ := synckey.Generate()
	m, _ := synckey.Mnemonic(key)
	words := strings.Fields(m)
	// Swap the first two words to break the checksum while keeping valid words.
	words[0], words[1] = words[1], words[0]
	if _, err := synckey.KeyFromMnemonic(strings.Join(words, " ")); err == nil {
		t.Error("expected an error for a tampered mnemonic")
	}
	if _, err := synckey.KeyFromMnemonic(""); err == nil {
		t.Error("expected an error for an empty mnemonic")
	}
}

// TestSyncURIRoundTrip verifies the QR pairing URI encodes and parses back.
func TestSyncURIRoundTrip(t *testing.T) {
	key, _ := synckey.Generate()
	uri, err := synckey.SyncURI(key)
	if err != nil {
		t.Fatalf("SyncURI: %v", err)
	}
	if !strings.HasPrefix(uri, "korai-sync:v1?k=") {
		t.Errorf("unexpected URI: %q", uri)
	}
	got, err := synckey.KeyFromURI(uri)
	if err != nil {
		t.Fatalf("KeyFromURI: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Error("URI did not round-trip")
	}
	if _, err := synckey.KeyFromURI("https://example/not-a-pairing-uri"); err == nil {
		t.Error("expected an error for a non-pairing URI")
	}
}

// TestRenderQR verifies QR rendering writes something scannable-looking without
// error.
func TestRenderQR(t *testing.T) {
	key, _ := synckey.Generate()
	var buf bytes.Buffer
	if err := synckey.RenderQR(&buf, key); err != nil {
		t.Fatalf("RenderQR: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("RenderQR produced no output")
	}
}

// TestRecoveryRoundTrip verifies the passphrase-wrapped blob unwraps only with
// the right passphrase.
func TestRecoveryRoundTrip(t *testing.T) {
	key, _ := synckey.Generate()
	blob, err := synckey.WrapRecovery(key, "correct horse battery staple")
	if err != nil {
		t.Fatalf("WrapRecovery: %v", err)
	}
	got, err := synckey.UnwrapRecovery(blob, "correct horse battery staple")
	if err != nil {
		t.Fatalf("UnwrapRecovery: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Error("recovery did not round-trip")
	}
	if _, err := synckey.UnwrapRecovery(blob, "wrong passphrase"); err == nil {
		t.Error("expected an error unwrapping with the wrong passphrase")
	}
	if _, err := synckey.WrapRecovery(key, "  "); err == nil {
		t.Error("expected an error wrapping with an empty passphrase")
	}
}

// TestExportRecovery verifies the blob is written to disk and unwraps.
func TestExportRecovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KORAI_SYNC_KEY", "")
	key, _ := synckey.Generate()
	if err := synckey.Save(home, key); err != nil {
		t.Fatal(err)
	}
	path, err := synckey.ExportRecovery(home, "pass phrase here")
	if err != nil {
		t.Fatalf("ExportRecovery: %v", err)
	}
	if path != synckey.RecoveryPath(home) {
		t.Errorf("path = %q, want %q", path, synckey.RecoveryPath(home))
	}
	data, err := readFile(t, path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := synckey.UnwrapRecovery(data, "pass phrase here")
	if err != nil {
		t.Fatalf("UnwrapRecovery of exported blob: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Error("exported recovery blob did not round-trip")
	}
}
