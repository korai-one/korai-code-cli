package synckey

import (
	"bytes"
	"testing"
)

// Cross-language interop vector. The SAME fixed key, sync_id, and mnemonic are
// asserted in the browser (packages/chat-ui/src/sync-crypto.test.ts in the korai
// repo) so any drift in the HMAC/base64 sync_id derivation or the BIP39 library
// on either side fails a test instead of silently breaking cross-surface
// teleport (korai-code-cli CLI and web kode-ui share this exact derivation to
// target the same hub namespace and decrypt each other's blocks).
//
// Key = bytes 0x00..0x1f (also the canonical BIP39 24-word test vector).
// If you change these constants, change them in sync-crypto.test.ts too — a
// mismatch means the two surfaces would no longer interoperate.
const (
	vectorSyncID   = "HqCF8twiJlg48JtBfFOpU08D1ScgWwtTWwbV5fePhBA"
	vectorMnemonic = "abandon amount liar amount expire adjust cage candy arch gather drum bullet absurd math era live bid rhythm alien crouch range attend journey unaware"
)

func fixedVectorKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func TestCrossLangVector(t *testing.T) {
	key := fixedVectorKey()

	if got := DeriveSyncID(key); got != vectorSyncID {
		t.Errorf("DeriveSyncID drift: got %q, want %q (MUST match browser deriveSyncId(key,\"korai-sync-id\"))", got, vectorSyncID)
	}

	mnem, err := Mnemonic(key)
	if err != nil {
		t.Fatal(err)
	}
	if mnem != vectorMnemonic {
		t.Errorf("Mnemonic drift: got %q, want %q (MUST match browser folderKeyToMnemonic)", mnem, vectorMnemonic)
	}

	back, err := KeyFromMnemonic(vectorMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, key) {
		t.Errorf("KeyFromMnemonic round-trip mismatch: got %x, want %x", back, key)
	}
}
