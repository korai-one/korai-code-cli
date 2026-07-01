package command_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/command"
)

func TestRevertCommand(t *testing.T) {
	t.Parallel()
	cmd := command.NewRevertCommand()
	if cmd.Name() != "revert" {
		t.Errorf("name = %q, want revert", cmd.Name())
	}

	// No argument reverts the most recent snapshot (empty Text).
	res, err := cmd.Run("")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := command.Result{Action: command.RevertSnapshot, Text: ""}
	if diff := cmp.Diff(want, res); diff != "" {
		t.Errorf("no-arg result mismatch (-want +got):\n%s", diff)
	}

	// A selector is passed through as Text.
	res, err = cmd.Run("2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want = command.Result{Action: command.RevertSnapshot, Text: "2"}
	if diff := cmp.Diff(want, res); diff != "" {
		t.Errorf("with-arg result mismatch (-want +got):\n%s", diff)
	}
}

func TestSnapshotsCommand(t *testing.T) {
	t.Parallel()
	cmd := command.NewSnapshotsCommand(func() string { return "snapshot list here" })
	if cmd.Name() != "snapshots" {
		t.Errorf("name = %q, want snapshots", cmd.Name())
	}

	res, err := cmd.Run("")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := command.Result{Action: command.ShowText, Text: "snapshot list here"}
	if diff := cmp.Diff(want, res); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}
