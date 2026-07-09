package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// tokenFilePerm keeps the stored tokens private to the user.
const tokenFilePerm os.FileMode = 0o600

// tokenDirPerm is the mode of the ~/.korai directory holding the token file.
const tokenDirPerm os.FileMode = 0o700

// refreshSkew is how long before actual expiry an access token is treated as
// stale, so a request never rides a token that expires mid-flight.
const refreshSkew = 60 * time.Second

// Token is the persisted credential set for a logged-in device. It is the whole
// content of ~/.korai/auth.json.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	// ExpiresAt is the access token's expiry as a Unix timestamp (seconds).
	ExpiresAt int64  `json:"expires_at"`
	TokenType string `json:"token_type"`
	// BaseURL records the orchestrator origin the tokens were minted against, so
	// a stored token is never replayed at a different deployment.
	BaseURL string `json:"base_url"`
}

// Path returns the on-disk location of the token file under home
// (~/.korai/auth.json).
func Path(home string) string {
	return filepath.Join(home, ".korai", "auth.json")
}

// LoadToken reads the stored token. ok is false with a nil error when no token
// file exists (the user has never logged in).
func LoadToken(home string) (t Token, ok bool, err error) {
	data, err := os.ReadFile(Path(home))
	if err != nil {
		if os.IsNotExist(err) {
			return Token{}, false, nil
		}
		return Token{}, false, fmt.Errorf("reading auth file: %w", err)
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return Token{}, false, fmt.Errorf("parsing auth file: %w", err)
	}
	return t, true, nil
}

// SaveToken persists t to Path(home) as 0600 in a 0700 dir, replacing any
// prior token. It writes to a temp file and renames so a crash mid-write cannot
// leave a truncated credential.
func SaveToken(home string, t Token) error {
	dir := filepath.Join(home, ".korai")
	if err := os.MkdirAll(dir, tokenDirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding auth: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "auth-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp auth file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds
	if err := tmp.Chmod(tokenFilePerm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp auth file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing auth: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp auth file: %w", err)
	}
	if err := os.Rename(tmpName, Path(home)); err != nil {
		return fmt.Errorf("replacing auth file: %w", err)
	}
	return nil
}

// DeleteToken removes the token file. A missing file is not an error, so logout
// is idempotent.
func DeleteToken(home string) error {
	if err := os.Remove(Path(home)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing auth file: %w", err)
	}
	return nil
}

// NeedsRefresh reports whether the access token is expired or within refreshSkew
// of expiring, so callers refresh before using it. A zero ExpiresAt is treated
// as "unknown, refresh".
func (t Token) NeedsRefresh() bool {
	if t.ExpiresAt == 0 {
		return true
	}
	return time.Now().Add(refreshSkew).Unix() >= t.ExpiresAt
}

// FromResponse folds a token-endpoint response into a stored Token, stamping the
// absolute expiry from the relative expires_in and recording baseURL.
func FromResponse(r TokenResponse, baseURL string) Token {
	return Token{
		AccessToken:  r.AccessToken,
		RefreshToken: r.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(r.ExpiresIn) * time.Second).Unix(),
		TokenType:    r.TokenType,
		BaseURL:      baseURL,
	}
}
