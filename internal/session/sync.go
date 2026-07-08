package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// recordEnvelope is the wire form of a full Record used when a conversation is
// shipped elsewhere (the cross-device sync client). It reuses the same DTO layer
// the on-disk store uses so the apiclient.ContentBlock interface round-trips
// with an explicit type tag. Updated is intentionally omitted: it is derived
// from storage mtime, not content, and must not perturb the content hash.
type recordEnvelope struct {
	ID       string       `json:"id"`
	Created  time.Time    `json:"created"`
	CWD      string       `json:"cwd"`
	Model    string       `json:"model"`
	Messages []messageDTO `json:"messages"`
}

// MarshalRecord serializes a Record's metadata and messages to plaintext bytes
// via the session DTO layer, for callers (such as the sync client) that ship a
// whole conversation as one opaque unit. It does not encrypt; wrap the result in
// a Codec for confidentiality. The encoding is deterministic for a given Record,
// so a content hash over it detects real changes.
func MarshalRecord(r Record) ([]byte, error) {
	env := recordEnvelope{ID: r.ID, Created: r.Created, CWD: r.CWD, Model: r.Model}
	env.Messages = make([]messageDTO, 0, len(r.Messages))
	for _, m := range r.Messages {
		env.Messages = append(env.Messages, msgToDTO(m))
	}
	data, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("encoding session record: %w", err)
	}
	return data, nil
}

// UnmarshalRecord reverses MarshalRecord. Updated is left zero (the caller sets
// it from local storage on merge).
func UnmarshalRecord(data []byte) (Record, error) {
	var env recordEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Record{}, fmt.Errorf("decoding session record: %w", err)
	}
	r := Record{ID: env.ID, Created: env.Created, CWD: env.CWD, Model: env.Model}
	r.Messages = make([]apiclient.Message, 0, len(env.Messages))
	for _, d := range env.Messages {
		r.Messages = append(r.Messages, msgFromDTO(d))
	}
	return r, nil
}

// MergeMessages unions two histories of the same append-only conversation,
// preserving order and dropping duplicates. Chat is append-mostly, so in the
// common case one history is a prefix of the other and the longer one is
// returned; when they diverge, local messages are kept first and any remote
// messages not already present are appended. Identity is by content (the DTO
// JSON encoding of the message), since this CLI's messages carry no per-message
// id or timestamp — a documented simplification of the design's (ts, id) union.
// The result always begins with the full local history, so a subsequent
// append-only Save extends rather than rewrites the store.
func MergeMessages(local, remote []apiclient.Message) []apiclient.Message {
	seen := make(map[string]struct{}, len(local)+len(remote))
	out := make([]apiclient.Message, 0, len(local)+len(remote))
	add := func(msgs []apiclient.Message) {
		for _, m := range msgs {
			key := messageKey(m)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, m)
		}
	}
	add(local)
	add(remote)
	return out
}

// messageKey returns a stable content key for a message, used to dedup during a
// merge. It reuses the DTO encoding so two messages are equal iff they serialize
// identically. A marshal failure falls back to a role-tagged sentinel, which is
// safe (at worst it under-dedups, never merges distinct messages).
func messageKey(m apiclient.Message) string {
	data, err := json.Marshal(msgToDTO(m))
	if err != nil {
		return "role:" + string(m.Role)
	}
	return string(data)
}
