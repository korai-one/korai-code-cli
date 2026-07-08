package session_test

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/session"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	k, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	return k
}

// TestEncryptingCodecRoundTrip covers encrypt/decrypt round-trip, a newline-free
// output (required by JSONL framing), and that plaintext does not leak.
func TestEncryptingCodecRoundTrip(t *testing.T) {
	t.Parallel()
	c, err := session.NewEncryptingCodec(testKey(t))
	if err != nil {
		t.Fatalf("NewEncryptingCodec: %v", err)
	}

	plain := []byte(`{"kind":"message","role":"user","secret":"attack at dawn"}`)
	enc, err := c.Encode(plain)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if bytes.ContainsRune(enc, '\n') {
		t.Errorf("Encode output contains a newline: %q", enc)
	}
	if bytes.Contains(enc, []byte("attack at dawn")) {
		t.Errorf("plaintext leaked into ciphertext: %q", enc)
	}

	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("round-trip mismatch: got %q want %q", got, plain)
	}
}

// TestEncryptingCodecNonceUnique verifies a fresh nonce per Encode: encrypting
// the same plaintext twice yields different ciphertexts that both decrypt.
func TestEncryptingCodecNonceUnique(t *testing.T) {
	t.Parallel()
	c, _ := session.NewEncryptingCodec(testKey(t))
	plain := []byte("same input")
	a, _ := c.Encode(plain)
	b, _ := c.Encode(plain)
	if bytes.Equal(a, b) {
		t.Error("two encryptions produced identical ciphertext (nonce reuse?)")
	}
}

// TestEncryptingCodecWrongKey verifies a wrong key fails AEAD authentication.
func TestEncryptingCodecWrongKey(t *testing.T) {
	t.Parallel()
	enc, _ := mustEncode(t, testKey(t), []byte("hello"))

	other := make([]byte, 32)
	other[0] = 0xff
	c2, _ := session.NewEncryptingCodec(other)
	if _, err := c2.Decode(enc); err == nil {
		t.Error("expected decryption under the wrong key to fail")
	}
}

// TestEncryptingCodecTamper verifies a modified ciphertext fails authentication.
func TestEncryptingCodecTamper(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	enc, c := mustEncode(t, key, []byte("hello world"))
	// Flip a byte in the middle of the base64 blob.
	enc[len(enc)/2] ^= 0x01
	if _, err := c.Decode(enc); err == nil {
		t.Error("expected tampered ciphertext to fail authentication")
	}
}

// TestEncryptingCodecBadKeyLength verifies the constructor rejects non-32-byte keys.
func TestEncryptingCodecBadKeyLength(t *testing.T) {
	t.Parallel()
	if _, err := session.NewEncryptingCodec([]byte("short")); err == nil {
		t.Error("expected error for a 5-byte key")
	}
}

func mustEncode(t *testing.T, key, plain []byte) ([]byte, *session.EncryptingCodec) {
	t.Helper()
	c, err := session.NewEncryptingCodec(key)
	if err != nil {
		t.Fatalf("NewEncryptingCodec: %v", err)
	}
	enc, err := c.Encode(plain)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return enc, c
}

// TestStoreSelectsCodecByName verifies both stores record the codec name in the
// header/row and select the matching codec on Load, and that a store lacking the
// codec cannot read the encrypted session.
func TestStoreSelectsCodecByName(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	msgs := sampleMessages()

	t.Run("file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		codec, _ := session.NewEncryptingCodec(key)
		store := session.NewFileStore(dir).WithCodec(codec)
		rec := session.Record{ID: "e", CWD: "/w", Model: "m", Messages: msgs}
		if err := store.Save(rec); err != nil {
			t.Fatalf("Save: %v", err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "e.jsonl"))
		if !strings.Contains(string(raw), `"enc":"`+session.EncryptingCodecName+`"`) {
			t.Errorf("header does not record codec name:\n%s", raw)
		}
		if strings.Contains(string(raw), "let me check") {
			t.Errorf("plaintext leaked to disk:\n%s", raw)
		}
		got, err := session.NewFileStore(dir).WithCodec(mustCodec(t, key)).Load("e")
		if err != nil {
			t.Fatalf("Load with codec: %v", err)
		}
		if diff := cmp.Diff(msgs, got.Messages); diff != "" {
			t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
		}
		if _, err := session.NewFileStore(dir).Load("e"); err == nil {
			t.Error("expected Load without codec to fail")
		}
	})

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "s.db")
		store, err := session.NewSQLiteStore(ctx, path)
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		store.WithCodec(mustCodec(t, key))
		rec := session.Record{ID: "e", Created: time.Now(), Updated: time.Now(), CWD: "/w", Model: "m", Messages: msgs}
		if err := store.Save(rec); err != nil {
			t.Fatalf("Save: %v", err)
		}
		_ = store.Close()

		// Reopen with the codec: decodes.
		s2, _ := session.NewSQLiteStore(ctx, path)
		s2.WithCodec(mustCodec(t, key))
		got, err := s2.Load("e")
		if err != nil {
			t.Fatalf("Load with codec: %v", err)
		}
		if diff := cmp.Diff(msgs, got.Messages); diff != "" {
			t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
		}
		_ = s2.Close()

		// Reopen without the codec: cannot decode.
		s3, _ := session.NewSQLiteStore(ctx, path)
		if _, err := s3.Load("e"); err == nil {
			t.Error("expected Load without codec to fail")
		}
		_ = s3.Close()
	})
}

func mustCodec(t *testing.T, key []byte) *session.EncryptingCodec {
	t.Helper()
	c, err := session.NewEncryptingCodec(key)
	if err != nil {
		t.Fatalf("NewEncryptingCodec: %v", err)
	}
	return c
}

// TestLoadContentKey covers the env and key-file sources plus the not-configured
// case.
func TestLoadContentKey(t *testing.T) {
	hexKey := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

	t.Run("env hex", func(t *testing.T) {
		t.Setenv("KORAI_SYNC_KEY", hexKey)
		k, ok, err := session.LoadContentKey(t.TempDir())
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if len(k) != 32 {
			t.Errorf("key len = %d, want 32", len(k))
		}
	})

	t.Run("not configured", func(t *testing.T) {
		t.Setenv("KORAI_SYNC_KEY", "")
		_, ok, err := session.LoadContentKey(t.TempDir())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("expected ok=false when no key is configured")
		}
	})

	t.Run("key file base64", func(t *testing.T) {
		t.Setenv("KORAI_SYNC_KEY", "")
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".korai"), 0o700); err != nil {
			t.Fatal(err)
		}
		raw, _ := hex.DecodeString(hexKey)
		// base64 std of the 32 bytes.
		if err := os.WriteFile(filepath.Join(home, ".korai", "sync.key"),
			[]byte("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="), 0o600); err != nil {
			t.Fatal(err)
		}
		k, ok, err := session.LoadContentKey(home)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if !bytes.Equal(k, raw) {
			t.Errorf("key mismatch: got %x want %x", k, raw)
		}
	})

	t.Run("bad env key", func(t *testing.T) {
		t.Setenv("KORAI_SYNC_KEY", "not-a-valid-key")
		if _, _, err := session.LoadContentKey(t.TempDir()); err == nil {
			t.Error("expected error for malformed key")
		}
	})
}

// TestMergeMessages covers the append-only union and dedup.
func TestMergeMessages(t *testing.T) {
	t.Parallel()
	full := sampleMessages()
	prefix := full[:1]

	// Remote has the full history, local only the prefix: merge yields full.
	got := session.MergeMessages(prefix, full)
	if diff := cmp.Diff(full, got); diff != "" {
		t.Errorf("prefix+full merge mismatch (-want +got):\n%s", diff)
	}

	// Idempotent: merging identical histories changes nothing.
	got = session.MergeMessages(full, full)
	if diff := cmp.Diff(full, got); diff != "" {
		t.Errorf("identical merge mismatch (-want +got):\n%s", diff)
	}

	// Divergence: local keeps its order, remote extras append.
	extra := []apiclient.Message{{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "extra"}}}}
	got = session.MergeMessages(full, extra)
	if len(got) != len(full)+1 {
		t.Errorf("union length = %d, want %d", len(got), len(full)+1)
	}
}

// TestMarshalRecordRoundTrip verifies the sync serialization round-trips.
func TestMarshalRecordRoundTrip(t *testing.T) {
	t.Parallel()
	rec := session.Record{
		ID: "r1", Created: time.Now().Truncate(time.Second),
		CWD: "/w", Model: "m", Messages: sampleMessages(),
	}
	data, err := session.MarshalRecord(rec)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}
	got, err := session.UnmarshalRecord(data)
	if err != nil {
		t.Fatalf("UnmarshalRecord: %v", err)
	}
	if got.ID != rec.ID || got.CWD != rec.CWD || got.Model != rec.Model {
		t.Errorf("metadata mismatch: %+v", got)
	}
	if diff := cmp.Diff(rec.Messages, got.Messages); diff != "" {
		t.Errorf("messages mismatch (-want +got):\n%s", diff)
	}
}
