package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Nevaero/korai-code-cli/internal/auth"
	"github.com/Nevaero/korai-code-cli/internal/command"
)

// loginTimeout caps a whole interactive login (browser round-trip or device-code
// polling). The device flow's own expiry is usually shorter (~10m); this is a
// backstop so a wedged flow cannot block forever.
const loginTimeout = 10 * time.Minute

// deviceLabel is the human-readable name this device registers under, shown in
// the web session list ("korai-cli @ <hostname>").
func deviceLabel() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	return "korai-cli @ " + host
}

// resolveBearer returns the credential to attach to networked inference. Order:
// (a) the login token in ~/.korai/auth.json — refreshed first (and the rotated
// pair persisted) when it is expired or near-expiry; on a revoked refresh family
// (invalid_grant) it forgets the token and falls through; (b) KORAI_API_KEY for
// CI / non-interactive use. It returns an error only when neither is available.
func resolveBearer(ctx context.Context, home, baseURL string) (string, error) {
	tok, ok, err := auth.LoadToken(home)
	if err != nil {
		// A corrupt auth file should not wedge startup; log and fall back to the
		// env key rather than failing.
		slog.Warn("ignoring unreadable auth file", "error", err)
		ok = false
	}
	if ok && tok.AccessToken != "" {
		// A token minted against a different orchestrator origin must not be
		// replayed here; ignore it and fall back to the env key.
		if tok.BaseURL != "" && tok.BaseURL != baseURL {
			slog.Warn("stored login token is for a different base URL; ignoring", "stored", tok.BaseURL, "active", baseURL)
		} else if !tok.NeedsRefresh() {
			return tok.AccessToken, nil
		} else if refreshed, rerr := refreshToken(ctx, home, baseURL, tok); rerr == nil {
			return refreshed, nil
		} else if auth.IsInvalidGrant(rerr) {
			_ = auth.DeleteToken(home)
			fmt.Fprintln(os.Stderr, "Your Korai session has expired or was revoked. Run `korai login` to sign in again.")
		} else {
			// Transient refresh failure (e.g. offline): try the existing access
			// token; if it too has expired the request will 401 and the user can
			// re-login. Better than hard-failing startup.
			slog.Warn("refreshing login token failed; using existing access token", "error", rerr)
			return tok.AccessToken, nil
		}
	}

	if key := strings.TrimSpace(os.Getenv("KORAI_API_KEY")); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("not authenticated: run `korai login`, or set KORAI_API_KEY (see .env.example)")
}

// refreshToken rotates the stored refresh token and persists the new pair,
// returning the fresh access token. The old refresh token is dead after this.
func refreshToken(ctx context.Context, home, baseURL string, tok auth.Token) (string, error) {
	tr, err := auth.NewClient(baseURL).Refresh(ctx, tok.RefreshToken)
	if err != nil {
		return "", err
	}
	next := auth.FromResponse(tr, baseURL)
	if serr := auth.SaveToken(home, next); serr != nil {
		return "", fmt.Errorf("persisting refreshed token: %w", serr)
	}
	return next.AccessToken, nil
}

// performLogin runs the login flow and persists the token, returning a
// success summary. useDevice forces the RFC 8628 device grant; otherwise the
// browser loopback flow is used, falling back to the device flow on a headless
// host. notify receives progress lines.
func performLogin(ctx context.Context, home string, useDevice bool, notify func(string)) (string, error) {
	baseURL := orchestratorBaseURL()
	client := auth.NewClient(baseURL)
	label := deviceLabel()

	var (
		tok auth.Token
		err error
	)
	if useDevice || !auth.BrowserLikelyAvailable() {
		tok, err = client.LoginDevice(ctx, label, notify)
	} else {
		tok, err = client.LoginBrowser(ctx, webBaseURL(), label, auth.OpenBrowser, notify)
	}
	if err != nil {
		return "", err
	}
	if serr := auth.SaveToken(home, tok); serr != nil {
		return "", serr
	}
	return fmt.Sprintf("Logged in as %s. Token stored in %s.", label, auth.Path(home)), nil
}

// performLogout revokes the stored refresh token (best effort) and deletes the
// token file. It is idempotent: a missing token reports "not logged in".
func performLogout(ctx context.Context, home string, notify func(string)) (string, error) {
	tok, ok, err := auth.LoadToken(home)
	if err != nil {
		// Even an unreadable file should be removable so the user can recover.
		_ = auth.DeleteToken(home)
		return "", fmt.Errorf("reading stored token: %w", err)
	}
	if !ok {
		return "Not logged in.", nil
	}
	if tok.RefreshToken != "" {
		base := tok.BaseURL
		if base == "" {
			base = orchestratorBaseURL()
		}
		if rerr := auth.NewClient(base).Revoke(ctx, tok.RefreshToken); rerr != nil {
			notify("Could not reach the server to revoke the token: " + rerr.Error())
		}
	}
	if derr := auth.DeleteToken(home); derr != nil {
		return "", derr
	}
	return "Logged out. Local token removed.", nil
}

// homeDir resolves the user's home directory, the root of ~/.korai.
func homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return home, nil
}

// loginCmd is `korai login`: authorize this device from an already-logged-in
// browser (the `gh auth login` pattern), storing this device's own rotating,
// revocable token.
func loginCmd() *cobra.Command {
	var useDevice bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authorize this device against your Korai account",
		Long: "Authorize this device against your Korai account by approving it from an\n" +
			"already-logged-in browser (the `gh auth login` pattern). The CLI stores its\n" +
			"own rotating, revocable token in ~/.korai/auth.json; it is not a copy of the\n" +
			"web token.\n\n" +
			"Use --device on a headless host (SSH, container): you get a short code to\n" +
			"enter at the verification URL from any browser.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := homeDir()
			if err != nil {
				return err
			}
			// Cap the flow and let Ctrl+C abort a pending browser/device wait.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, loginTimeout)
			defer cancel()
			out := cmd.OutOrStdout()
			notify := func(s string) { fmt.Fprintln(out, s) }
			msg, lerr := performLogin(ctx, home, useDevice, notify)
			if lerr != nil {
				return lerr
			}
			fmt.Fprintln(out, msg)
			return nil
		},
	}
	cmd.Flags().BoolVar(&useDevice, "device", false,
		"use the device-code flow (headless/SSH): enter a code at a URL instead of opening a browser")
	return cmd
}

// logoutCmd is `korai logout`: revoke this device's token and remove it locally.
func logoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "logout",
		Short:         "Revoke this device's token and sign out",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := homeDir()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			notify := func(s string) { fmt.Fprintln(out, s) }
			msg, lerr := performLogout(cmd.Context(), home, notify)
			if lerr != nil {
				return lerr
			}
			fmt.Fprintln(out, msg)
			return nil
		},
	}
	return cmd
}

// loginSlashCommand is the REPL's /login. It runs the same flow as `korai
// login`, synchronously, and reports the outcome as text. Progress lines are
// folded into the returned text since the command layer has no streaming
// channel.
type loginSlashCommand struct{ home string }

// newLoginCommand builds the /login slash command rooted at the given home dir.
func newLoginCommand(home string) command.Command { return &loginSlashCommand{home: home} }

func (*loginSlashCommand) Name() string { return "login" }
func (*loginSlashCommand) Description() string {
	return "authorize this device against your Korai account (/login device for headless)"
}

// Run performs the login. "device" (or "--device") as the argument forces the
// headless device-code flow. It blocks until the flow completes or times out.
func (c *loginSlashCommand) Run(args string) (command.Result, error) {
	useDevice := false
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "device", "--device":
		useDevice = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()

	var lines []string
	notify := func(s string) { lines = append(lines, s) }
	msg, err := performLogin(ctx, c.home, useDevice, notify)
	if err != nil {
		lines = append(lines, "Login failed: "+err.Error())
		return command.Result{Action: command.ShowText, Text: strings.Join(lines, "\n")}, nil
	}
	lines = append(lines, msg, "Restart the session (or start a new turn) for the new credential to take effect.")
	return command.Result{Action: command.ShowText, Text: strings.Join(lines, "\n")}, nil
}

// logoutSlashCommand is the REPL's /logout.
type logoutSlashCommand struct{ home string }

// newLogoutCommand builds the /logout slash command rooted at the given home dir.
func newLogoutCommand(home string) command.Command { return &logoutSlashCommand{home: home} }

func (*logoutSlashCommand) Name() string        { return "logout" }
func (*logoutSlashCommand) Description() string { return "revoke this device's token and sign out" }

// Run revokes and forgets the stored token.
func (c *logoutSlashCommand) Run(string) (command.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var lines []string
	notify := func(s string) { lines = append(lines, s) }
	msg, err := performLogout(ctx, c.home, notify)
	if err != nil {
		lines = append(lines, "Logout failed: "+err.Error())
		return command.Result{Action: command.ShowText, Text: strings.Join(lines, "\n")}, nil
	}
	lines = append(lines, msg)
	return command.Result{Action: command.ShowText, Text: strings.Join(lines, "\n")}, nil
}
