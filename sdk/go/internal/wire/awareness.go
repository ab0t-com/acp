package wire

import "encoding/json"

// Awareness types (acp-ext-9 Appendix A) — the ephemeral per-(actor, session)
// presence tier. ADDITIVE to the acp/1 schema: no existing type changes and
// ProtocolVersion does NOT bump; support is negotiated via the "awareness"
// capability. Awareness entries never appear in Event, snapshots, or any
// replicated structure (ext-9 §3.1).

// AwarenessSet is the POST /v1/awareness request body (ext-9 §4.2).
//
// State is a json.RawMessage so the three wire cases stay distinguishable:
// absent (nil → 400), explicit null ("null" → leave), or an object. TTLSec
// is a pointer for the same reason: absent (nil → daemon default) differs
// from an explicit 0 (leave only alongside an empty-object state; otherwise
// non-positive → 400).
type AwarenessSet struct {
	State   json.RawMessage `json:"state"`
	TTLSec  *int            `json:"ttl_sec,omitempty"`
	Session string          `json:"session,omitempty"`
}

// AwarenessEntry is one live entry: the POST response and the snapshot /
// stream element (ext-9 §4.1, Appendix A). Actor is always token-derived by
// the daemon (§8.1). A leave delta MAY carry only actor and session.
type AwarenessEntry struct {
	Actor   string          `json:"actor"`
	Session string          `json:"session"`
	State   json.RawMessage `json:"state,omitempty"`
	TTLSec  int             `json:"ttl_sec,omitempty"`
	Updated string          `json:"updated,omitempty"` // RFC3339, serving node's clock
	Expires string          `json:"expires,omitempty"` // RFC3339, serving node's clock
}

// AwarenessSnapshot is the GET /v1/awareness response (ext-9 §4.3).
type AwarenessSnapshot struct {
	Entries []AwarenessEntry `json:"entries"`
}

// AwarenessDelta is one NDJSON line of GET /v1/awareness?follow=true
// (ext-9 §4.4): type is "join" | "update" | "leave". Deltas carry no seq and
// no replay cursor — a reconnecting follower re-primes from the snapshot or
// the stream's synthetic joins.
type AwarenessDelta struct {
	Type  string         `json:"type"`
	Entry AwarenessEntry `json:"entry"`
}

// AwarenessFrame is one text frame of the ext-9 §11.3 WebSocket binding —
// a single discriminated union with "op" as the tag. Per §11.6 the Appendix A
// shapes are reused VERBATIM inside frames: a "set" frame carries exactly the
// AwarenessSet fields (§4.2 semantics, bounds, and errors), a "delta" frame
// exactly the AwarenessDelta fields (§4.4). "ping"/"pong" carry only the op.
// The actor is ALWAYS token-derived (§8.1) — no frame field can claim one.
type AwarenessFrame struct {
	Op string `json:"op"` // "set" | "delta" | "ping" | "pong"

	// set (client → server): the AwarenessSet fields.
	State   json.RawMessage `json:"state,omitempty"` // null = explicit leave
	TTLSec  *int            `json:"ttl_sec,omitempty"`
	Session string          `json:"session,omitempty"`

	// delta (server → client): the AwarenessDelta fields.
	Type  string          `json:"type,omitempty"`
	Entry *AwarenessEntry `json:"entry,omitempty"`
}
