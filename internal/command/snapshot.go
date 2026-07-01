package command

import "strings"

// revertCommand asks the host to restore the working tree to a prior snapshot,
// undoing recent agent file changes.
type revertCommand struct{}

// NewRevertCommand returns a /revert command. With no argument it restores the
// most recent snapshot; with a selector (e.g. how many steps back) it asks the
// host to restore that snapshot.
func NewRevertCommand() Command { return &revertCommand{} }

// Name returns "revert".
func (*revertCommand) Name() string { return "revert" }

// Description returns the command summary.
func (*revertCommand) Description() string {
	return "undo recent file changes by restoring a snapshot (optionally pass how many steps back)"
}

// Run signals the host to restore a snapshot, passing the optional selector
// (empty Text means the most recent snapshot).
func (*revertCommand) Run(args string) (Result, error) {
	return Result{Action: RevertSnapshot, Text: strings.TrimSpace(args)}, nil
}

// snapshotsCommand lists the available snapshots.
type snapshotsCommand struct{ list func() string }

// NewSnapshotsCommand returns a /snapshots command that shows the snapshot
// listing produced by list.
func NewSnapshotsCommand(list func() string) Command {
	return &snapshotsCommand{list: list}
}

// Name returns "snapshots".
func (*snapshotsCommand) Name() string { return "snapshots" }

// Description returns the command summary.
func (*snapshotsCommand) Description() string { return "list available snapshots" }

// Run renders the snapshot listing.
func (c *snapshotsCommand) Run(string) (Result, error) {
	return Result{Action: ShowText, Text: c.list()}, nil
}
