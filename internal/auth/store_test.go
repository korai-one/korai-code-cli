package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	home := t.TempDir()
	want := Token{
		AccessToken:  "jwt.abc.def",
		RefreshToken: "krr_sid.secret",
		ExpiresAt:    time.Now().Add(15 * time.Minute).Unix(),
		TokenType:    "Bearer",
		BaseURL:      "https://korai-eu.fly.dev",
	}
	if err := SaveToken(home, want); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	got, ok, err := LoadToken(home)
	if err != nil || !ok {
		t.Fatalf("LoadToken: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}

	// The file must be 0600 on POSIX; Windows does not carry Unix perms.
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(Path(home))
		if statErr != nil {
			t.Fatal(statErr)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("auth.json perm = %o, want 600", perm)
		}
	}
}

func TestLoadTokenMissing(t *testing.T) {
	_, ok, err := LoadToken(t.TempDir())
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if ok {
		t.Error("expected ok=false for a home with no auth.json")
	}
}

func TestDeleteTokenIdempotent(t *testing.T) {
	home := t.TempDir()
	// Deleting a non-existent token is not an error.
	if err := DeleteToken(home); err != nil {
		t.Fatalf("DeleteToken (absent): %v", err)
	}
	if err := SaveToken(home, Token{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteToken(home); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if _, err := os.Stat(Path(home)); !os.IsNotExist(err) {
		t.Error("auth.json still present after DeleteToken")
	}
}

func TestNeedsRefresh(t *testing.T) {
	cases := []struct {
		name string
		exp  int64
		want bool
	}{
		{"zero expiry", 0, true},
		{"already expired", time.Now().Add(-time.Minute).Unix(), true},
		{"within skew", time.Now().Add(30 * time.Second).Unix(), true},
		{"fresh", time.Now().Add(10 * time.Minute).Unix(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Token{ExpiresAt: tc.exp}).NeedsRefresh(); got != tc.want {
				t.Errorf("NeedsRefresh() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFromResponse(t *testing.T) {
	before := time.Now().Unix()
	tok := FromResponse(TokenResponse{
		AccessToken:  "a",
		RefreshToken: "r",
		TokenType:    "Bearer",
		ExpiresIn:    900,
	}, "https://x")
	if tok.AccessToken != "a" || tok.RefreshToken != "r" || tok.BaseURL != "https://x" {
		t.Errorf("fields not copied: %+v", tok)
	}
	if tok.ExpiresAt < before+900 || tok.ExpiresAt > time.Now().Unix()+900 {
		t.Errorf("ExpiresAt = %d not ~= now+900", tok.ExpiresAt)
	}
}

// TestPath keeps the on-disk location stable (~/.korai/auth.json).
func TestPath(t *testing.T) {
	if got, want := Path("/home/u"), filepath.Join("/home/u", ".korai", "auth.json"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}
