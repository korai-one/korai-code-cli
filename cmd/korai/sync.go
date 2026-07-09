package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Nevaero/korai-code-cli/internal/synckey"
)

// syncCmd groups the cross-device history-sync key commands. Sync stays off by
// default; these commands are the opt-in on-ramp (generate/adopt a key and turn
// it on).
func syncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Manage cross-device history sync (opt-in, off by default)",
		Long: "Cross-device history sync replicates your conversations through a blind\n" +
			"hub as end-to-end encrypted blobs. The hub only ever sees ciphertext under a\n" +
			"key it never receives. These commands manage that key and turn sync on.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(syncSetupCmd(), syncJoinCmd(), syncQRCmd(), syncExportRecoveryCmd())
	return cmd
}

// syncSetupCmd generates the key on the first device, prints the transports to
// carry it to other devices, shows the derived namespace, and enables sync.
func syncSetupCmd() *cobra.Command {
	var url string
	cmd := &cobra.Command{
		Use:           "setup",
		Short:         "Generate the sync key, print its mnemonic + QR, and enable sync",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locating home directory: %w", err)
			}
			out := cmd.OutOrStdout()

			key, ok, err := synckey.Load(home)
			if err != nil {
				return err
			}
			if !ok {
				if key, err = synckey.Generate(); err != nil {
					return err
				}
				if err := synckey.Save(home, key); err != nil {
					return err
				}
				fmt.Fprintf(out, "Generated a new 256-bit sync key at %s\n", synckey.KeyPath(home))
			} else {
				fmt.Fprintf(out, "Using the existing sync key at %s\n", synckey.KeyPath(home))
			}

			if err := printKeyTransports(out, key); err != nil {
				return err
			}
			path, err := enableSyncSettings(home, url)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\nSync enabled in %s.\n", path)
			if url == "" && strings.TrimSpace(os.Getenv("KORAI_SYNC_URL")) == "" {
				_, _ = fmt.Fprintln(out, "Set the hub URL with --url or the KORAI_SYNC_URL env var to start syncing.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "hub base URL to record in settings (or set KORAI_SYNC_URL)")
	return cmd
}

// syncJoinCmd adopts an existing namespace on a second device from its mnemonic.
func syncJoinCmd() *cobra.Command {
	var url string
	cmd := &cobra.Command{
		Use:           "join",
		Short:         "Adopt an existing sync namespace from its 24-word mnemonic",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locating home directory: %w", err)
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprint(out, "Enter the 24-word recovery mnemonic:\n> ")
			line, err := readLine(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("reading mnemonic: %w", err)
			}
			key, err := synckey.KeyFromMnemonic(line)
			if err != nil {
				return err
			}
			if err := synckey.Save(home, key); err != nil {
				return err
			}
			path, err := enableSyncSettings(home, url)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\nAdopted namespace %s\n", synckey.DeriveSyncID(key))
			fmt.Fprintf(out, "Key saved to %s; sync enabled in %s.\n", synckey.KeyPath(home), path)
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "hub base URL to record in settings (or set KORAI_SYNC_URL)")
	return cmd
}

// syncQRCmd re-displays the terminal QR for the current key.
func syncQRCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "qr",
		Short:         "Display the terminal QR code for the current sync key",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locating home directory: %w", err)
			}
			key, ok, err := synckey.Load(home)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no sync key configured; run `korai sync setup` first")
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, "Scan to adopt this sync namespace on another device:")
			return synckey.RenderQR(out, key)
		},
	}
}

// syncExportRecoveryCmd wraps the key under a passphrase and writes it locally.
func syncExportRecoveryCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "export-recovery",
		Short:         "Export a passphrase-wrapped recovery blob for the current key",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locating home directory: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprint(out, "Enter a recovery passphrase (guards only this blob):\n> ")
			pass, err := readLine(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("reading passphrase: %w", err)
			}
			path, err := synckey.ExportRecovery(home, pass)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\nRecovery blob written to %s (keep it somewhere safe).\n", path)
			_, _ = fmt.Fprintln(out, "Note: hub-stored recovery (recover with no surviving device) is a documented follow-up; this export is local only.")
			return nil
		},
	}
}

// printKeyTransports prints the mnemonic and the terminal QR plus the derived
// sync_id for a key.
func printKeyTransports(out io.Writer, key []byte) error {
	mnemonic, err := synckey.Mnemonic(key)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "\nWrite these 24 words down to add another device (`korai sync join`):")
	fmt.Fprintf(out, "\n  %s\n\n", mnemonic)
	fmt.Fprintln(out, "Or scan this QR from a co-present device:")
	if err := synckey.RenderQR(out, key); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nNamespace (sync_id): %s\n", synckey.DeriveSyncID(key))
	return nil
}

// enableSyncSettings flips sync on in the user settings file, preserving every
// other key. When url is non-empty it is recorded too. It returns the settings
// path for reporting.
func enableSyncSettings(home, url string) (string, error) {
	path := filepath.Join(home, ".korai", "settings.json")
	top := map[string]json.RawMessage{}
	if data, rerr := os.ReadFile(path); rerr == nil {
		if uerr := json.Unmarshal(data, &top); uerr != nil {
			return "", fmt.Errorf("parsing %s: %w", path, uerr)
		}
	} else if !os.IsNotExist(rerr) {
		return "", fmt.Errorf("reading %s: %w", path, rerr)
	}

	sync := map[string]any{}
	if raw, ok := top["sync"]; ok {
		_ = json.Unmarshal(raw, &sync)
	}
	sync["enabled"] = true
	if url != "" {
		sync["url"] = url
	}
	syncRaw, err := json.Marshal(sync)
	if err != nil {
		return "", fmt.Errorf("encoding sync settings: %w", err)
	}
	top["sync"] = syncRaw

	blob, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("creating settings dir: %w", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// readLine reads a single trimmed line from r. It is used for interactive
// prompts (mnemonic, passphrase, nuke code) so secrets arrive on stdin rather
// than as arguments that would land in shell history.
func readLine(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
