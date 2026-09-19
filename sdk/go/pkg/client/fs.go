package client

// fs.go — the realtime directory tree (ext-28) + filesystem mode (ext-31)
// client surface: thin, typed wrappers over the daemon's /v1/fs/* and
// /v1/space/config endpoints. The DAEMON owns every guard (mode gate, write
// scope, the bulk-delete disposition, the flip's exact-count confirm); this SDK
// never retries a refusal on its own and never fills a confirm — it surfaces
// each refusal as an *APIError with the stable "code" (see Code and the
// FSMode*/WrongFSMode helpers) so a CLI/bridge can turn it into a decision.
//
// Surfaces wrapped (cmd/coordd/fs.go + fsmode.go; frozen wire, consumed as-is):
//
//	GET  /v1/space/config         FSMode           any role     the space's mode
//	PUT  /v1/space/config         SetFSMode        admin        flip cas<->realtime (confirm-capable)
//	GET  /v1/fs/tree              FSTree           reader       live tree (read_prefix-filtered)
//	POST /v1/fs/ops               FSOps + helpers  writer       create/mkdir/rename/move/delete/restore
//	GET  /v1/fs/trash             FSTrash          reader       recoverable trashed roots
//	GET  /v1/fs/trash?checkpoint= FSTrashAtCheckpoint reader    pre-flip trash of a now-CAS space
//	POST /v1/fs/trash/restore     FSTrashRestoreToCAS writer    recover pre-flip trash into the CAS manifest
//	POST /v1/admin/fs/prune       FSPrune          admin        evict trash past its window (records only)
//
// POST /v1/admin/fs/mode is the daemon's NO-CONFIRM alias of the PUT flip (it
// delegates to the same handler and answers 428 for a non-empty space); the SDK
// exposes the confirm-capable PUT only, so there is exactly one flip method and
// it can always satisfy the exact-count contract.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// Code returns the stable machine-readable "code" of a refusal body ("" when
// the body carries none) — e.g. wrong_fs_mode, bulk_denied,
// fs_mode_confirm_required, leases_held, live_docs_unflushed. Prefer the typed
// helpers where one exists; Code is the general escape hatch.
func (e *APIError) Code() string {
	var b struct {
		Code string `json:"code"`
	}
	json.Unmarshal(e.Raw, &b)
	return b.Code
}

// WrongFSMode reports a 409 wrong_fs_mode: the call went to the surface the
// space's mode does not serve (a /v1/fs/* call on a CAS space, or /v1/commit
// on a realtime space). There is NO shadow state — nothing was written. Re-read
// the mode (FSMode) and use the other surface; never retry the same call.
func (e *APIError) WrongFSMode() bool {
	return e.Status == http.StatusConflict && e.Code() == "wrong_fs_mode"
}

// FSModeConfirmRequired reports a 428 fs_mode_confirm_required: the flip would
// cross a NON-EMPTY region and carried no confirm. Not a 409 — a client MUST
// NOT rebase-and-retry; it shows the FSModeDemand and, only after a human or an
// explicit automation flag agrees with those EXACT numbers, re-sends the same
// flip with a wire.FSModeConfirm echoing them.
func (e *APIError) FSModeConfirmRequired() bool {
	return e.Status == http.StatusPreconditionRequired && e.Code() == "fs_mode_confirm_required"
}

// FSModeConfirmMismatch reports a 428 fs_mode_confirm_mismatch: the confirm did
// not match the daemon's (re-evaluated, in-apply) count — the region changed
// under the caller. Show both numbers and re-decide; never re-echo the new ones.
func (e *APIError) FSModeConfirmMismatch() bool {
	return e.Status == http.StatusPreconditionRequired && e.Code() == "fs_mode_confirm_mismatch"
}

// FSModeDemand extracts the "confirm" block of a 428 flip refusal (what the flip
// would cross). ok is false if the body carries none.
func (e *APIError) FSModeDemand() (wire.FSModeDemand, bool) {
	var b struct {
		Confirm *wire.FSModeDemand `json:"confirm"`
	}
	if json.Unmarshal(e.Raw, &b) == nil && b.Confirm != nil {
		return *b.Confirm, true
	}
	return wire.FSModeDemand{}, false
}

// requireCrdtfs gates the realtime-tree calls on the daemon's advertised
// capability, so an older coordd yields one clear error instead of a bare 404.
func (c *Client) requireCrdtfs() error {
	ok, err := c.hasCapability("crdtfs")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("daemon lacks crdtfs capability (older coordd) — the realtime tree surfaces are not served; use the commit/manifest surfaces or upgrade the daemon")
	}
	return nil
}

// requireFsmode gates the mode read/flip on the ext-31 capability.
func (c *Client) requireFsmode() error {
	ok, err := c.hasCapability("fsmode")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("daemon lacks fsmode capability (older coordd) — every space is CAS; upgrade the daemon to use realtime mode")
	}
	return nil
}

// FSMode returns the space's filesystem mode — wire.FSModeCAS or
// wire.FSModeRealtime (ext-31 §4.1 negotiation read, any role). Read it before
// choosing a surface: CAS = commit/manifest; realtime = the FS* calls.
func (c *Client) FSMode() (string, error) {
	if err := c.requireFsmode(); err != nil {
		return "", err
	}
	var out wire.FSModeResult
	if err := c.do("GET", "/v1/space/config", nil, &out); err != nil {
		return "", err
	}
	if out.FSMode == "" {
		return wire.FSModeCAS, nil
	}
	return out.FSMode, nil
}

// SetFSMode flips the space's mode (ADMIN role; ext-31 §4.3). mode is
// wire.FSModeCAS or wire.FSModeRealtime. An EMPTY region flips with confirm
// nil. A non-empty region is refused 428 fs_mode_confirm_required until the
// SAME call is re-sent with confirm echoing the daemon's demand EXACTLY
// (FSModeDemand: region "/", from, to, entries); the daemon re-checks the count
// in apply and refuses 428 fs_mode_confirm_mismatch if the region moved. Other
// refusals (each 409, no state change): leases_held (cas->realtime with held
// leases), live_docs_unflushed (realtime->cas with live docs — flush them to
// blobs first), unrepresentable_path / unrepresentable_manifest (rename the
// named paths), checkpoint_cap_at_capacity (the mandatory pre-flip pin).
//
// A flip is a one-way boundary for the OTHER surface's guarantees: read the
// result's FSMode (NoOp=true means this request did not flip) and the pinned
// Checkpoint — the pre-flip state stays recoverable through it for the
// retention window.
func (c *Client) SetFSMode(mode string, confirm *wire.FSModeConfirm) (wire.FSModeResult, error) {
	var out wire.FSModeResult
	if err := c.requireFsmode(); err != nil {
		return out, err
	}
	if mode != wire.FSModeCAS && mode != wire.FSModeRealtime {
		return out, fmt.Errorf("fs mode must be %q or %q, got %q", wire.FSModeCAS, wire.FSModeRealtime, mode)
	}
	body := map[string]any{"fs_mode": mode}
	if confirm != nil {
		body["confirm"] = confirm
	}
	return out, c.do("PUT", "/v1/space/config", body, &out)
}

// FSTree returns the space's live realtime tree (reader role), every node with
// its materialized path, node id, and content reference; filtered to the
// token's read_prefix (an out-of-scope node is simply absent — no oracle). A
// CAS-mode space answers 409 wrong_fs_mode (see (*APIError).WrongFSMode).
func (c *Client) FSTree() ([]wire.FSNode, error) {
	if err := c.requireCrdtfs(); err != nil {
		return nil, err
	}
	var out struct {
		Nodes []wire.FSNode `json:"nodes"`
	}
	if err := c.do("GET", "/v1/fs/tree", nil, &out); err != nil {
		return nil, err
	}
	if out.Nodes == nil {
		out.Nodes = []wire.FSNode{}
	}
	return out.Nodes, nil
}

// FSOps applies one ATOMIC, ordered batch of structural ops (writer role; see
// wire.FSOp for the vocabulary). Concurrent disjoint batches from other agents
// all land — there is no CAS and never a 409 for a structural race; the CRDT
// converges (add-wins create, last-stamp-wins placement, sticky-trash delete).
// Failures are per-batch and atomic: 400 for an unresolvable path / unknown
// op, 403 for a write-scope refusal, 403 bulk_denied when the batch's DELETE
// count reaches the space's bulk minimum and the grant is not allowed bulk
// (NOT confirmable — the caller reduces the batch or has an admin apply it),
// 409 wrong_fs_mode on a CAS space, 503 (Retry-After) for a leader-local blob
// stat fault on create. Check the result's Outstamped: those ops had no effect.
//
// No idempotency key in v1: a retry after a commit timeout can double a
// create/rename (HZ-ACPDB-29) — re-read the tree before retrying.
func (c *Client) FSOps(ops []wire.FSOp) (wire.FSOpsResult, error) {
	var out wire.FSOpsResult
	if err := c.requireCrdtfs(); err != nil {
		return out, err
	}
	if len(ops) == 0 {
		return out, fmt.Errorf("fs ops: empty batch")
	}
	return out, c.do("POST", "/v1/fs/ops", map[string]any{"ops": ops}, &out)
}

// FSMkdir creates folders (each parent must already exist; order the batch
// parent-first — a later op sees an earlier op's effect).
func (c *Client) FSMkdir(paths ...string) (wire.FSOpsResult, error) {
	ops := make([]wire.FSOp, 0, len(paths))
	for _, p := range paths {
		ops = append(ops, wire.FSOp{Op: "mkdir", Path: p})
	}
	return c.FSOps(ops)
}

// FSCreate places a NEW file node at path referencing an already-uploaded blob
// (PutBlob) or a live text document by name (exactly one of blob/doc). A create
// at an existing path does not fail: both nodes exist and the loser is shown as
// "<name>~<nodeId>" (ext-28 §4.2.4) — check FSTree first when that is not wanted.
// Content is immutable per node: to replace a blob-backed file, delete it and
// create the new one (the old bytes stay recoverable in trash).
func (c *Client) FSCreate(path, blob, doc string) (wire.FSOpsResult, error) {
	if (blob == "") == (doc == "") {
		return wire.FSOpsResult{}, fmt.Errorf("fs create %s: exactly one of blob hash or doc name is required", path)
	}
	return c.FSOps([]wire.FSOp{{Op: "create", Path: path, Blob: blob, Doc: doc}})
}

// FSMove moves or renames a node (its whole subtree follows; node ids are
// stable, only paths change). The destination's parent folder must exist.
func (c *Client) FSMove(from, to string) (wire.FSOpsResult, error) {
	return c.FSOps([]wire.FSOp{{Op: "move", Path: from, To: to}})
}

// FSDelete moves each named node (and its subtree) to TRASH — recoverable via
// FSTrash + FSRestore for the space's retention window, never an immediate
// erase. One op per path; the daemon's bulk guard counts these ops.
func (c *Client) FSDelete(paths ...string) (wire.FSOpsResult, error) {
	ops := make([]wire.FSOp, 0, len(paths))
	for _, p := range paths {
		ops = append(ops, wire.FSOp{Op: "delete", Path: p})
	}
	return c.FSOps(ops)
}

// FSRestore brings a trashed ROOT (a node id from FSTrash) back into the live
// tree — at `to` (its parent must exist), or at its original origin path when
// to is "". Its whole subtree returns with it. A node that is not a restorable
// trashed root (a subtree member, an evicted id, or one outside the token's
// scope) is refused 400 — missing-shaped by design.
func (c *Client) FSRestore(nodeID, to string) (wire.FSOpsResult, error) {
	return c.FSOps([]wire.FSOp{{Op: "restore", Node: nodeID, To: to}})
}

// FSTrash lists the recoverable trashed roots of a REALTIME space (reader
// role): the restore handle (NodeID), the immutable pre-trash Origin path and
// when it was trashed; filtered by origin to the token's read_prefix.
func (c *Client) FSTrash() ([]wire.FSTrashEntry, error) {
	if err := c.requireCrdtfs(); err != nil {
		return nil, err
	}
	return c.fsTrash("/v1/fs/trash")
}

// FSTrashAtCheckpoint lists the RETAINED PRE-FLIP trash of a space that has
// since flipped realtime->cas (ext-31 §4.7.4 — the CAS-mode recovery door; the
// plain FSTrash is 409 wrong_fs_mode on a CAS space). checkpoint is the flip's
// auto pin (auto/fsmode-realtime-to-cas-v<N>, from Checkpoints or the flip
// result). Trashed FILES are listed recursively, each with its reconstructed
// origin, and are individually restorable via FSTrashRestoreToCAS. A
// still-realtime space or an unknown/non-flip checkpoint is refused (409/400).
func (c *Client) FSTrashAtCheckpoint(checkpoint string) ([]wire.FSTrashEntry, error) {
	if err := c.requireFsmode(); err != nil {
		return nil, err
	}
	if checkpoint == "" {
		return nil, fmt.Errorf("fs trash: a flip checkpoint name is required")
	}
	return c.fsTrash("/v1/fs/trash?checkpoint=" + url.QueryEscape(checkpoint))
}

func (c *Client) fsTrash(path string) ([]wire.FSTrashEntry, error) {
	var out struct {
		Trash []wire.FSTrashEntry `json:"trash"`
	}
	if err := c.do("GET", path, nil, &out); err != nil {
		return nil, err
	}
	if out.Trash == nil {
		out.Trash = []wire.FSTrashEntry{}
	}
	return out.Trash, nil
}

// FSTrashRestoreToCAS recovers ONE pre-flip trashed FILE (a node id from
// FSTrashAtCheckpoint) into the CURRENT CAS manifest at its origin path, as an
// ordinary forward commit (writer role; its blob is still GC-pinned). The space
// stays CAS — no flip-back, no shadow tree. A folder is recovered by restoring
// its files. Refusals: 409 wrong_fs_mode (the space is realtime — use
// FSRestore), 400 for an unknown checkpoint / non-restorable node, 403 for a
// write-scope refusal, 409 manifest conflict (rebase: just call again).
func (c *Client) FSTrashRestoreToCAS(nodeID, checkpoint string) (wire.FSTrashRestoreResult, error) {
	var out wire.FSTrashRestoreResult
	if err := c.requireFsmode(); err != nil {
		return out, err
	}
	if nodeID == "" || checkpoint == "" {
		return out, fmt.Errorf("fs trash restore: node id and flip checkpoint are required")
	}
	return out, c.do("POST", "/v1/fs/trash/restore", map[string]any{"node_id": nodeID, "checkpoint": checkpoint}, &out)
}

// FSPrune proposes one leader-driven eviction pass NOW (ADMIN role): trashed
// roots whose retention window has expired are evicted (their records; blob
// GC reclaims the bytes later), never a live, pinned or unclassifiable node.
// The daemon runs this on its own cadence; call it to reclaim on demand.
func (c *Client) FSPrune() (wire.FSPruneResult, error) {
	var out wire.FSPruneResult
	if err := c.requireCrdtfs(); err != nil {
		return out, err
	}
	return out, c.do("POST", "/v1/admin/fs/prune", nil, &out)
}
