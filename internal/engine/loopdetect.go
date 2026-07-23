package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// loopDetector tracks repeated identical tool calls within a single Run so the
// engine can break the classic agent failure mode: re-issuing the same call
// forever because its result did not change.
//
// A "repeat" is the same tool name with normalized-identical arguments whose
// previous execution produced the same volatile-stripped result (timestamps,
// UUIDs, and long digit runs are stripped before comparing, so a clock or a
// request id changing does not mask a genuine no-progress loop). A repeat whose
// result DID change is progress — e.g. re-running `git status` after an edit —
// and resets the streak.
//
// Escalation per (tool, args) key:
//
//	streak 1 → the call runs silently.
//	streak 2 → the call runs, but a corrective notice is appended to its result.
//	streak 3 → the call is vetoed: skipped, with a synthetic error result.
//
// Two vetoes in one Run (across any keys) make the engine force the graceful
// wrap-up (see Engine.wrapUp).
//
// The detector is created per Run and only touched from the run goroutine, so
// it needs no locking.
type loopDetector struct {
	entries map[string]*loopEntry
	vetoes  int
}

// loopEntry is the per-(tool, args) state.
type loopEntry struct {
	// streak counts consecutive identical calls whose stripped results matched.
	streak int
	// lastResult is the volatile-stripped result of the previous execution.
	lastResult string
}

func newLoopDetector() *loopDetector {
	return &loopDetector{entries: make(map[string]*loopEntry)}
}

// check classifies an upcoming call BEFORE execution. It returns true when the
// call must be vetoed (the caller skips execution and surfaces a synthetic
// error result), incrementing the run's veto count.
func (d *loopDetector) check(name string, args json.RawMessage) (veto bool) {
	e := d.entries[callKey(name, args)]
	if e != nil && e.streak >= 2 {
		d.vetoes++
		return true
	}
	return false
}

// observe records an executed call's result and reports whether the caller
// should append the corrective warn notice: true exactly when this execution
// made the no-progress streak reach 2 (the second identical call with an
// identical stripped result). A changed result resets the streak — that call
// made progress.
func (d *loopDetector) observe(name string, args json.RawMessage, result string) (warn bool) {
	key := callKey(name, args)
	stripped := stripVolatile(result)
	e := d.entries[key]
	if e == nil {
		d.entries[key] = &loopEntry{streak: 1, lastResult: stripped}
		return false
	}
	if e.lastResult == stripped {
		e.streak++
	} else {
		e.streak = 1
	}
	e.lastResult = stripped
	return e.streak == 2
}

// vetoCount returns how many calls this Run has vetoed so far.
func (d *loopDetector) vetoCount() int { return d.vetoes }

// loopWarnNotice is appended to the result of the second identical no-progress
// call, steering the model away before a veto becomes necessary.
const loopWarnNotice = "\n\n[loop detector] This exact tool call was already made and returned the same result. Do not repeat it — change the arguments or the approach."

// loopVetoMessage is the synthetic error result for a vetoed call.
func loopVetoMessage(name string) string {
	return fmt.Sprintf("tool call skipped: this exact %s call was already made twice with identical results. Repeating it will not make progress — change your approach: use different arguments, a different tool, or answer with what you already know.", name)
}

// callKey builds the detector key for a call: the tool name plus a hash of its
// normalized arguments.
func callKey(name string, args json.RawMessage) string {
	return name + "\x00" + normalizeArgs(args)
}

// normalizeArgs canonicalizes a JSON argument payload — decode and re-encode,
// which sorts object keys and squeezes whitespace — so formatting differences
// cannot hide a repeat, then hashes it. Unparsable input hashes its trimmed raw
// bytes instead.
func normalizeArgs(raw json.RawMessage) string {
	canon := []byte(strings.TrimSpace(string(raw)))
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if m, err := json.Marshal(v); err == nil {
			canon = m
		}
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

// Volatile patterns stripped from results before comparing them, so that
// incidental churn (clocks, request ids, PIDs, epoch stamps) does not disguise
// a no-progress repeat as new output.
var (
	volatileUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	// volatileTimestamp matches ISO-8601-ish date-times with optional fraction
	// and zone (2026-07-23T14:03:59.123Z, 2026-07-23 14:03:59+02:00, …).
	volatileTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	// volatileDigits matches long digit runs (epoch seconds/millis, PIDs,
	// counters). Five digits is long enough to spare line numbers and years.
	volatileDigits = regexp.MustCompile(`\d{5,}`)
)

// stripVolatile replaces volatile substrings with stable placeholders.
// Timestamps are stripped before digit runs so a fractional-second tail cannot
// split the match.
func stripVolatile(s string) string {
	s = volatileUUID.ReplaceAllString(s, "<uuid>")
	s = volatileTimestamp.ReplaceAllString(s, "<ts>")
	s = volatileDigits.ReplaceAllString(s, "<n>")
	return s
}
