package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/korai-one/korai-sdk-go/session/synchub"

	"github.com/Nevaero/korai-code-cli/internal/config"
	"github.com/Nevaero/korai-code-cli/internal/synckey"
)

// errNukeRejected is returned when the entered code does not match the verifier.
// Its message is deliberately generic and identical whether or not a nuke is
// even configured, so running the command reveals nothing about its state.
var errNukeRejected = errors.New("nuke: code rejected")

// nukeCmd is the duress "nuke": entering the pre-set code irreversibly destroys
// all synced history, locally and on the hub, with NO confirmation (the code IS
// the confirmation). `korai nuke set` arms it first.
func nukeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nuke",
		Short: "Duress wipe: enter the code to irreversibly destroy all synced history",
		Long: "Duress wipe (GrapheneOS pattern). Arm it once with `korai nuke set`, storing\n" +
			"only an Argon2id verifier — never the code. Running `korai nuke` then reads the\n" +
			"code from stdin; if it matches, it IMMEDIATELY crypto-shreds the sync key,\n" +
			"purges local history, and best-effort deletes the hub namespace. There is no\n" +
			"confirmation prompt: the code is the confirmation.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runNuke,
	}
	cmd.AddCommand(nukeSetCmd())
	return cmd
}

// nukeSetCmd stores the Argon2id verifier for the duress code.
func nukeSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "set",
		Aliases:       []string{"arm"},
		Short:         "Set (arm) the duress nuke code",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locating home directory: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprint(out, "Set a duress nuke code (distinct from any password you use normally):\n> ")
			code, err := readLine(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("reading nuke code: %w", err)
			}
			if err := synckey.SetNukeVerifier(home, code); err != nil {
				return err
			}
			fmt.Fprintf(out, "\nNuke armed. Verifier stored at %s. Entering this code with `korai nuke` wipes everything.\n",
				synckey.NukeVerifierPath(home))
			return nil
		},
	}
}

// runNuke reads the code from stdin, verifies it, and on a match performs the
// wipe with no confirmation. A mismatch (or no configured verifier) does nothing
// and exits non-zero without revealing which case occurred.
func runNuke(cmd *cobra.Command, _ []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locating home directory: %w", err)
	}
	out := cmd.OutOrStdout()
	fmt.Fprint(out, "Enter nuke code: ")
	code, err := readLine(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("reading nuke code: %w", err)
	}

	ok, err := synckey.VerifyNukeCode(home, code)
	if err != nil {
		// A genuine I/O/parse error is not the same as a mismatch; surface it.
		return err
	}
	if !ok {
		return errNukeRejected
	}

	wd, _ := os.Getwd()
	report := performNuke(cmd.Context(), home, wd)
	fmt.Fprintf(out, "\nNuke complete. Key shredded=%t; %d path(s) removed; remote wiped=%t.\n",
		report.KeyShredded, len(report.Removed), report.RemoteWiped)
	for _, e := range report.Errs {
		// Best-effort steps: report but do not fail — the crypto-shred already
		// made remote ciphertext unreadable.
		fmt.Fprintf(out, "  (non-fatal) %v\n", e)
	}
	return nil
}

// performNuke resolves the wipe surface and hub client, then runs synckey.Wipe.
func performNuke(ctx context.Context, home, projectDir string) synckey.WipeReport {
	key, _, _ := synckey.Load(home) // may be nil already; Wipe tolerates it

	var remotePurge func(context.Context) error
	if url := resolveHubURL(home); url != "" && len(key) == synckey.KeyLen {
		client := synchub.NewClient(url, synckey.DeriveSyncID(key), nil)
		remotePurge = client.WipeRemote
	}

	return synckey.Wipe(ctx, key, synckey.DefaultWipePaths(home, projectDir), remotePurge)
}

// resolveHubURL finds the hub base URL from the env or the user settings block,
// so the remote purge can target the right namespace. Empty means skip the
// remote step (the crypto-shred still protects the data).
func resolveHubURL(home string) string {
	if v := strings.TrimSpace(os.Getenv("KORAI_SYNC_URL")); v != "" {
		return v
	}
	settings, err := config.DefaultPaths(home, home).Load()
	if err != nil || settings.Sync == nil {
		return ""
	}
	return strings.TrimSpace(settings.Sync.URL)
}
