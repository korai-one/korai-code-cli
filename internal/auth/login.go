package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// callbackPath is the fixed loopback redirect path. The orchestrator
// exact-matches scheme+host+path (127.0.0.1 / [::1] / localhost, any port,
// path /callback), so this must not change.
const callbackPath = "/callback"

// LoginBrowser runs the Authorization Code + PKCE loopback flow: it stands up an
// ephemeral 127.0.0.1 listener, opens webURL/authorize in the browser, waits for
// the browser to redirect back with the code, and exchanges it at the
// orchestrator for a token pair. webURL is the WEB consent origin (KORAI_WEB_URL,
// e.g. https://korai.one); the CLI's own BaseURL is the orchestrator. open is
// the browser opener (injected for tests); notify receives human-readable
// progress lines.
func (c *Client) LoginBrowser(ctx context.Context, webURL, deviceLabel string, open func(string) error, notify func(string)) (Token, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return Token{}, err
	}
	state, err := GenerateState()
	if err != nil {
		return Token{}, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Token{}, fmt.Errorf("starting loopback listener: %w", err)
	}
	redirectURI := fmt.Sprintf("http://%s%s", ln.Addr().String(), callbackPath)

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != callbackPath {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			if e := q.Get("error"); e != "" {
				writeCallbackPage(w, false)
				results <- result{err: &Error{Code: e, Description: q.Get("error_description")}}
				return
			}
			if q.Get("state") != state {
				writeCallbackPage(w, false)
				results <- result{err: errors.New("state mismatch on callback (possible CSRF); aborting")}
				return
			}
			code := q.Get("code")
			if code == "" {
				writeCallbackPage(w, false)
				results <- result{err: errors.New("callback carried no authorization code")}
				return
			}
			writeCallbackPage(w, true)
			results <- result{code: code}
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		// Shutdown needs a live context: the caller's ctx may already be cancelled
		// (that is often how the flow ends). WithoutCancel keeps the lineage (so it
		// is not a fresh root) while dropping that cancellation.
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	authURL := buildAuthorizeURL(webURL, redirectURI, pkce.Challenge, state, deviceLabel)
	notify("Opening your browser to authorize this device.")
	notify("If it does not open, visit:\n  " + authURL)
	if open != nil {
		if oerr := open(authURL); oerr != nil {
			notify("Could not launch a browser automatically: " + oerr.Error())
		}
	}
	notify("Waiting for authorization…")

	select {
	case <-ctx.Done():
		return Token{}, ctx.Err()
	case res := <-results:
		if res.err != nil {
			return Token{}, res.err
		}
		tr, xerr := c.ExchangeCode(ctx, res.code, pkce.Verifier, redirectURI, deviceLabel)
		if xerr != nil {
			return Token{}, fmt.Errorf("exchanging authorization code: %w", xerr)
		}
		return FromResponse(tr, c.BaseURL), nil
	}
}

// LoginDevice runs the RFC 8628 device grant for headless/SSH shells: it starts
// the grant, shows the user_code and verification URI, and polls the token
// endpoint respecting interval/slow_down until approved or expired.
func (c *Client) LoginDevice(ctx context.Context, deviceLabel string, notify func(string)) (Token, error) {
	da, err := c.StartDevice(ctx, DefaultScope, deviceLabel)
	if err != nil {
		return Token{}, fmt.Errorf("starting device authorization: %w", err)
	}
	notify(fmt.Sprintf("Go to %s and enter code: %s", da.VerificationURI, da.UserCode))
	if da.VerificationURIComplete != "" {
		notify("Or open directly: " + da.VerificationURIComplete)
	}
	notify("Waiting for approval…")

	interval := time.Duration(da.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(da.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return Token{}, ctx.Err()
		case <-time.After(interval):
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return Token{}, errors.New("device code expired before approval; run login again")
		}
		tr, perr := c.PollDevice(ctx, da.DeviceCode, deviceLabel)
		if perr == nil {
			return FromResponse(tr, c.BaseURL), nil
		}
		var oe *Error
		if !errors.As(perr, &oe) {
			return Token{}, perr
		}
		switch oe.Code {
		case ErrAuthorizationPending:
			// keep polling at the current interval
		case ErrSlowDown:
			interval = nextPollInterval(interval, oe.Code)
		case ErrExpiredToken:
			return Token{}, errors.New("device code expired before approval; run login again")
		case ErrAccessDenied:
			return Token{}, errors.New("authorization was denied")
		default:
			return Token{}, perr
		}
	}
}

// slowDownStep is the RFC 8628 §3.5 increment added to the poll interval each
// time the server answers slow_down.
const slowDownStep = 5 * time.Second

// nextPollInterval returns the poll interval to use after an error code. A
// slow_down bumps the interval by slowDownStep; any other code leaves it
// unchanged. Pulled out so the backoff is unit-testable without a server.
func nextPollInterval(cur time.Duration, code string) time.Duration {
	if code == ErrSlowDown {
		return cur + slowDownStep
	}
	return cur
}

// buildAuthorizeURL assembles the web consent URL the browser is pointed at.
func buildAuthorizeURL(webURL, redirectURI, challenge, state, deviceLabel string) string {
	base := webURL
	// Trim a trailing slash so we don't emit "//authorize".
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	q := url.Values{
		"client_id":             {ClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"scope":                 {DefaultScope},
		"device_label":          {deviceLabel},
	}
	return base + "/authorize?" + q.Encode()
}

// writeCallbackPage renders the minimal page the browser lands on after the
// redirect, telling the user to return to the terminal.
func writeCallbackPage(w http.ResponseWriter, ok bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	msg := "Authorization complete. You can close this tab and return to the terminal."
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Authorization failed. Return to the terminal for details."
	}
	_, _ = fmt.Fprintf(w, "<!doctype html><html><head><meta charset=utf-8><title>Korai CLI</title></head>"+
		"<body style=\"font-family:system-ui;max-width:32rem;margin:4rem auto;text-align:center\">"+
		"<h1>Korai CLI</h1><p>%s</p></body></html>", msg)
}
