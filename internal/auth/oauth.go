package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Well-known OAuth error codes returned on /oauth/token and /oauth/device. The
// device-grant polling codes drive the poll loop; the rest are terminal.
const (
	ErrAuthorizationPending = "authorization_pending"
	ErrSlowDown             = "slow_down"
	ErrExpiredToken         = "expired_token"
	ErrAccessDenied         = "access_denied"
	ErrInvalidGrant         = "invalid_grant"
)

// TokenResponse is the shared success body of POST /oauth/token.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// DeviceAuth is the success body of POST /oauth/device (RFC 8628 §3.2).
type DeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Error is an /oauth/* error body: {"error","error_description"}. Callers switch
// on Code (e.g. ErrAuthorizationPending) to drive the flow.
type Error struct {
	Code        string
	Description string
	// Status is the HTTP status the error arrived with (some codes, notably
	// invalid_grant on refresh reuse, are only distinguishable as 401).
	Status int
}

func (e *Error) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

// IsInvalidGrant reports whether err is an OAuth invalid_grant — the signal that
// the whole refresh family is revoked and the user must log in again.
func IsInvalidGrant(err error) bool {
	var oe *Error
	if errors.As(err, &oe) {
		return oe.Code == ErrInvalidGrant
	}
	return false
}

// Client calls the orchestrator's public /oauth/* endpoints. BaseURL is the
// orchestrator origin (KORAI_BASE_URL), NOT the web consent origin.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient builds an OAuth client for the orchestrator at baseURL with a
// sensible request timeout. The device-poll loop uses per-request timeouts, so
// a long poll never wedges on a single hung request.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ExchangeCode runs grant_type=authorization_code: it redeems a single-use auth
// code plus the PKCE verifier for an access+refresh pair.
func (c *Client) ExchangeCode(ctx context.Context, code, verifier, redirectURI, deviceLabel string) (TokenResponse, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"client_id":     {ClientID},
		"redirect_uri":  {redirectURI},
		"device_label":  {deviceLabel},
	})
}

// Refresh runs grant_type=refresh_token. The response carries a NEW refresh
// token (the presented one is now dead); a superseded token yields a 401
// invalid_grant (whole family revoked) — check with IsInvalidGrant.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (TokenResponse, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

// PollDevice runs one grant_type=device_code poll. On the not-yet-approved path
// it returns an *Error whose Code is ErrAuthorizationPending or ErrSlowDown.
func (c *Client) PollDevice(ctx context.Context, deviceCode, deviceLabel string) (TokenResponse, error) {
	return c.token(ctx, url.Values{
		"grant_type":   {"device_code"},
		"device_code":  {deviceCode},
		"client_id":    {ClientID},
		"device_label": {deviceLabel},
	})
}

// StartDevice begins an RFC 8628 device grant, returning the user_code and
// verification URIs to show the operator.
func (c *Client) StartDevice(ctx context.Context, scope, deviceLabel string) (DeviceAuth, error) {
	body := url.Values{
		"client_id":    {ClientID},
		"scope":        {scope},
		"device_label": {deviceLabel},
	}
	resp, err := c.postForm(ctx, "/oauth/device", body)
	if err != nil {
		return DeviceAuth{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return DeviceAuth{}, parseError(resp.StatusCode, data)
	}
	var da DeviceAuth
	if err := json.Unmarshal(data, &da); err != nil {
		return DeviceAuth{}, fmt.Errorf("decoding device response: %w", err)
	}
	return da, nil
}

// Revoke revokes a refresh token's whole session family (RFC 7009). The endpoint
// always returns 200, so this only errors on a transport failure.
func (c *Client) Revoke(ctx context.Context, token string) error {
	resp, err := c.postForm(ctx, "/oauth/revoke", url.Values{"token": {token}})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// token POSTs the form to /oauth/token and decodes the success or error body.
func (c *Client) token(ctx context.Context, body url.Values) (TokenResponse, error) {
	resp, err := c.postForm(ctx, "/oauth/token", body)
	if err != nil {
		return TokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return TokenResponse{}, parseError(resp.StatusCode, data)
	}
	var tr TokenResponse
	if err := json.Unmarshal(data, &tr); err != nil {
		return TokenResponse{}, fmt.Errorf("decoding token response: %w", err)
	}
	return tr, nil
}

// postForm sends a urlencoded POST to the given path on the orchestrator.
func (c *Client) postForm(ctx context.Context, path string, body url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", path, err)
	}
	return resp, nil
}

// parseError turns a non-200 /oauth/* body into an *Error, falling back to the
// HTTP status when the body is not the expected {"error",...} shape.
func parseError(status int, data []byte) error {
	var e struct {
		Code        string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(data, &e); err == nil && e.Code != "" {
		return &Error{Code: e.Code, Description: e.Description, Status: status}
	}
	return &Error{Code: fmt.Sprintf("http_%d", status), Description: strings.TrimSpace(string(data)), Status: status}
}
