package command

import (
	"errors"
	"strings"
	"testing"
)

// fakeWorkerSelector is a WorkerSelector stub for the /worker_mode command.
type fakeWorkerSelector struct {
	mode      string
	available []string
	setErr    error // returned by SetMode; when nil the switch is recorded
	setCalls  []string
}

func (f *fakeWorkerSelector) Mode() string        { return f.mode }
func (f *fakeWorkerSelector) Available() []string { return f.available }
func (f *fakeWorkerSelector) SetMode(m string) error {
	f.setCalls = append(f.setCalls, m)
	if f.setErr != nil {
		return f.setErr
	}
	f.mode = m
	return nil
}

func TestWorkerCommandStatus(t *testing.T) {
	t.Parallel()
	sel := &fakeWorkerSelector{mode: "local", available: []string{"local", "remote"}}
	cmd := NewWorkerCommand(sel)

	res, err := cmd.Run("")
	if err != nil {
		t.Fatalf("Run(\"\"): %v", err)
	}
	if res.Action != ShowText {
		t.Fatalf("Action = %v, want ShowText", res.Action)
	}
	if len(sel.setCalls) != 0 {
		t.Fatalf("status listing must not switch: setCalls=%v", sel.setCalls)
	}
	// The active mode is marked; both modes are listed.
	if !strings.Contains(res.Text, "* local") || !strings.Contains(res.Text, "remote") {
		t.Fatalf("status text missing marker/modes:\n%s", res.Text)
	}
}

func TestWorkerCommandSwitch(t *testing.T) {
	t.Parallel()
	sel := &fakeWorkerSelector{mode: "local", available: []string{"local", "remote"}}
	cmd := NewWorkerCommand(sel)

	res, err := cmd.Run("remote")
	if err != nil {
		t.Fatalf("Run(remote): %v", err)
	}
	if want := []string{"remote"}; len(sel.setCalls) != 1 || sel.setCalls[0] != "remote" {
		t.Fatalf("setCalls = %v, want %v", sel.setCalls, want)
	}
	if !strings.Contains(res.Text, "switched inference to remote") {
		t.Fatalf("unexpected switch text:\n%s", res.Text)
	}
}

func TestWorkerCommandSwitchError(t *testing.T) {
	t.Parallel()
	sel := &fakeWorkerSelector{
		mode:      "remote",
		available: []string{"remote"},
		setErr:    errors.New("no local worker available"),
	}
	cmd := NewWorkerCommand(sel)

	res, err := cmd.Run("local")
	if err != nil {
		t.Fatalf("Run(local): %v", err)
	}
	// A rejected switch surfaces the error and still shows the status.
	if !strings.Contains(res.Text, "no local worker available") {
		t.Fatalf("error text missing:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "Inference backend:") {
		t.Fatalf("status not appended after error:\n%s", res.Text)
	}
}
