package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	korai "github.com/korai-one/korai-sdk-go"

	"github.com/Nevaero/korai-code-cli/internal/config"
	"github.com/Nevaero/korai-code-cli/internal/session"
)

// teleportCmd resumes a conversation that started on ANOTHER Korai surface. It is
// the cross-surface face of resume: it opens the shared canonical session store
// (the same one --resume/--continue/`/resume` read), lists what has synced in —
// grouping by the originating Tool and putting other-surface sessions first — and
// hands the chosen id to the ordinary interactive resume path. With an explicit
// id it skips the picker and resumes that session directly.
func teleportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teleport [session-id]",
		Short: "Resume a conversation that started on another device or surface (e.g. the web app)",
		Long: "Resume a synced conversation that originated elsewhere — the kode web UI, the\n" +
			"dashboard, or another device running this CLI.\n\n" +
			"With no argument it lists the synced sessions, marking the ones that did NOT\n" +
			"start in this CLI so cross-surface conversations are obvious, and lets you pick\n" +
			"one. With a session id it resumes that session directly. Either way it reuses\n" +
			"the same resume path as --resume, so the conversation continues in the REPL.\n\n" +
			"Sync must be configured (see `korai sync setup`) for other-surface sessions to\n" +
			"arrive locally.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				id := strings.TrimSpace(args[0])
				if id == "" {
					return fmt.Errorf("empty session id")
				}
				// The TUI owns the screen, so route logs away from stderr.
				setupLogging(false, io.Discard)
				return runTUI(cmd.Context(), runOptions{resumeID: id})
			}
			return runTeleportPicker(cmd)
		},
	}
	return cmd
}

// runTeleportPicker lists synced sessions, prompts for a selection, and resumes
// the chosen one through the interactive resume path.
func runTeleportPicker(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locating home directory: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	// Load settings so the store is opened with the sync at-rest codec: sessions
	// synced in from other surfaces are stored encrypted and only decode (and so
	// only list) when the codec is applied.
	settings, err := config.DefaultPaths(home, wd).Load()
	if err != nil {
		return fmt.Errorf("loading settings: %w", err)
	}
	store, _ := openSyncedStore(cmd.Context(), home, syncFileSettings(settings.Sync))

	sessions, err := store.List()
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}
	ordered := orderForTeleport(sessions)
	if len(ordered) == 0 {
		fmt.Fprintln(out, "No sessions to teleport yet. Configure sync (`korai sync setup`) and let another device push a conversation first.")
		return nil
	}

	fmt.Fprint(out, formatTeleportList(ordered))
	fmt.Fprint(out, "\nEnter a number to teleport (blank to cancel): ")
	line, err := readLine(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("reading selection: %w", err)
	}
	if strings.TrimSpace(line) == "" {
		fmt.Fprintln(out, "Cancelled.")
		return nil
	}
	idx, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || idx < 1 || idx > len(ordered) {
		return fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
	}
	chosen := ordered[idx-1]

	// The TUI owns the screen, so route logs away from stderr before launching it.
	setupLogging(false, io.Discard)
	return runTUI(cmd.Context(), runOptions{resumeID: chosen.ID})
}

// orderForTeleport puts sessions that did NOT originate in this CLI first (the
// whole point of teleport), preserving the store's newest-first order within each
// group. The result is a stable partition, so a session's relative recency is
// kept inside its group.
func orderForTeleport(sessions []korai.Session) []korai.Session {
	other := make([]korai.Session, 0, len(sessions))
	mine := make([]korai.Session, 0, len(sessions))
	for _, s := range sessions {
		if s.Tool == session.Tool {
			mine = append(mine, s)
		} else {
			other = append(other, s)
		}
	}
	return append(other, mine...)
}

// formatTeleportList renders the numbered teleport menu: each row shows the
// selection number, the originating tool, a first-line snippet, the message
// count, the update time, and a short id. Other-surface sessions are marked so
// they stand out from ones this CLI produced.
func formatTeleportList(sessions []korai.Session) string {
	var b strings.Builder
	b.WriteString("Teleport — resume a conversation from any device or surface:\n")
	for i, s := range sessions {
		tool := s.Tool
		if tool == "" {
			tool = "unknown"
		}
		marker := "  "
		origin := ""
		if s.Tool != session.Tool {
			marker = "▸ "
			origin = "  ⟵ other surface"
		}
		fmt.Fprintf(&b, "\n %s%2d. [%-14s] %-52s %3d msgs  %s  %s%s",
			marker, i+1, tool, firstUserText(s.Messages), len(s.Messages),
			s.Updated.Local().Format("2006-01-02 15:04"), shortID(s.ID), origin)
	}
	b.WriteString("\n")
	return b.String()
}

// shortID trims a session id to a compact display form (the leading 8 runes),
// enough to recognize it without dominating the row.
func shortID(id string) string {
	const n = 8
	if len(id) <= n {
		return id
	}
	return id[:n] + "…"
}
