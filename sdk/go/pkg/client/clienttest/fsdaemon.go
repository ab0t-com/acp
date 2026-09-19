// Package clienttest is TEST SUPPORT for ACP client code: an in-memory,
// httptest-hosted stand-in for the realtime-tree (ext-28) + filesystem-mode
// (ext-31) surfaces of coordd, so a client, CLI or bridge test can drive the
// exact request/response CONTRACT (paths, status codes, stable "code" values,
// body shapes) without a real daemon, disk, TLS or token minting. It is NOT a
// coordd and carries none of its semantics beyond the wire contract: no CRDT
// convergence, no replication, no retention clock, no scope filtering.
//
// The contract mirrored is cmd/coordd/fs.go + fsmode.go as of ext-28/ext-31
// Stage 2 (see CONFORMANCE-ext28.md / CONFORMANCE-ext31.md). Each handler
// comment names the daemon behaviour it copies; if the daemon's wire changes,
// this file is the client side's canary and must change with it.
//
// Fail-safe: every instance is a 127.0.0.1 httptest server closed by t.Cleanup;
// nothing touches a real daemon, :8443, /srv/acp or ~/.acp.
package clienttest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// Grant is what a bearer token maps to in the fake: a role and the ext-34 bulk
// disposition (a writer is denied bulk by default, exactly like coordd).
type Grant struct {
	Role      string // admin | writer | reader
	AllowBulk bool
}

type node struct {
	id      string
	name    string
	parent  string // "" = root
	folder  bool
	blob    string
	size    int64
	doc     string
	trashed bool
	origin  string // pre-trash path (set on delete)
	trashAt int64
}

// FSDaemon is the fake. Fields are exported for a test to arrange state
// directly (a fixture) and to assert on what the client sent.
type FSDaemon struct {
	TS *httptest.Server

	mu       sync.Mutex
	Grants   map[string]Grant // bearer token -> grant
	Mode     string           // "cas" | "realtime"
	HasTree  bool             // the space ever had tree state (a flipped-to-cas region keeps its trash)
	Manifest map[string]wire.ManifestEntry
	Version  uint64
	nodes    map[string]*node
	nextID   int
	clock    int64
	Checkpts map[string]uint64 // name -> version
	// DelMin is the ext-34 bulk minimum for a single DELETE batch (coordd default 10).
	DelMin uint32
	// Outstamp, when set, makes the next fs.ops answer carry an "outstamped"
	// warning for every op (the daemon's R7 report of a no-effect op).
	Outstamp bool
	// Calls counts requests per METHOD+path (query stripped), for "never
	// retried" assertions.
	Calls map[string]int
	// Blobs holds blobs uploaded via POST /v1/blobs (hash -> bytes).
	Blobs map[string][]byte
}

// NewFSDaemon starts a fake with three tokens: "admin" (admin), "writer"
// (writer, bulk DENIED) and "reader" (reader), a CAS-mode empty space.
func NewFSDaemon(t *testing.T) *FSDaemon {
	t.Helper()
	d := &FSDaemon{
		Grants:   map[string]Grant{"admin": {Role: "admin", AllowBulk: true}, "writer": {Role: "writer"}, "bulkwriter": {Role: "writer", AllowBulk: true}, "reader": {Role: "reader"}},
		Mode:     "cas",
		Manifest: map[string]wire.ManifestEntry{},
		nodes:    map[string]*node{},
		Checkpts: map[string]uint64{},
		DelMin:   10,
		Calls:    map[string]int{},
		Blobs:    map[string][]byte{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "capabilities": []string{"acpuri", "awareness", "bulkguard", "channels", "crdtfs", "crdtjson", "fsmode", "history", "quotas", "scopedtokens"}})
	})
	mux.HandleFunc("/v1/stats", d.auth("reader", d.handleStats))
	mux.HandleFunc("/v1/manifest", d.auth("reader", d.handleManifest))
	mux.HandleFunc("/v1/blobs", d.auth("writer", d.handleBlobPut))
	mux.HandleFunc("/v1/checkpoint", d.auth("reader", d.handleCheckpoints))
	mux.HandleFunc("/v1/space/config", d.auth("reader", d.handleSpaceConfig))
	mux.HandleFunc("/v1/admin/fs/mode", d.auth("admin", d.handleAdminMode))
	mux.HandleFunc("/v1/admin/fs/prune", d.auth("admin", d.handlePrune))
	mux.HandleFunc("/v1/fs/tree", d.auth("reader", d.handleTree))
	mux.HandleFunc("/v1/fs/trash", d.auth("reader", d.handleTrash))
	mux.HandleFunc("/v1/fs/trash/restore", d.auth("writer", d.handleTrashRestore))
	mux.HandleFunc("/v1/fs/ops", d.auth("writer", d.handleOps))
	d.TS = httptest.NewServer(mux)
	t.Cleanup(d.TS.Close)
	return d
}

// URL is the fake's base URL (http://127.0.0.1:<port>).
func (d *FSDaemon) URL() string { return d.TS.URL }

// --- fixtures -------------------------------------------------------------

// SeedRealtime puts the space in realtime mode with the given folders and
// files (path -> blob hash), parents created implicitly. Returns nothing; read
// ids back through Tree.
func (d *FSDaemon) SeedRealtime(folders []string, files map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Mode, d.HasTree = "realtime", true
	for _, f := range folders {
		d.ensureFolderLocked(f)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		parent, name := splitPath(p)
		pid := d.ensureFolderLocked(parent)
		d.newNodeLocked(pid, name, false, files[p], "")
	}
}

// Tree returns the live tree the fake would serve (for assertions).
func (d *FSDaemon) Tree() []wire.FSNode {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.treeLocked()
}

// TrashIDs returns the trashed ROOT ids by origin (for assertions).
func (d *FSDaemon) TrashIDs() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]string{}
	for _, n := range d.nodes {
		if n.trashed {
			out[n.origin] = n.id
		}
	}
	return out
}

// --- internals -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, wire.ErrorResponse{Error: msg})
}

var roleRank = map[string]int{"reader": 1, "writer": 2, "admin": 3}

// auth mirrors coordd's bearer + role gate: 401 for a bad token, 403 below the
// minimum role. It also refuses any credential-shaped query (the SDK never
// puts a token in a URL — a regression here is a leak).
func (d *FSDaemon) auth(minRole string, h func(http.ResponseWriter, *http.Request, Grant)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.Calls[r.Method+" "+r.URL.Path]++
		d.mu.Unlock()
		if q := r.URL.Query(); q.Get("token") != "" || q.Get("access_token") != "" {
			writeErr(w, 400, "credential in URL")
			return
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		g, ok := d.Grants[tok]
		if !ok {
			writeErr(w, 401, "unauthorized")
			return
		}
		if roleRank[g.Role] < roleRank[minRole] {
			writeErr(w, 403, "requires the "+minRole+" role")
			return
		}
		h(w, r, g)
	}
}

func splitPath(p string) (string, string) {
	p = strings.Trim(p, "/")
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

func (d *FSDaemon) mint() string {
	d.nextID++
	d.clock++
	return strconv.Itoa(d.nextID) + "-fake"
}

func (d *FSDaemon) newNodeLocked(parent, name string, folder bool, blob, doc string) *node {
	n := &node{id: d.mint(), name: name, parent: parent, folder: folder, blob: blob, doc: doc}
	if blob != "" {
		n.size = int64(len(d.Blobs[blob]))
		if n.size == 0 {
			n.size = int64(len(blob)) // a fixture hash with no uploaded bytes still gets a size
		}
	}
	d.nodes[n.id] = n
	return n
}

// pathOfLocked materializes a LIVE node's path ("" for root).
func (d *FSDaemon) pathOfLocked(n *node) string {
	if n.parent == "" {
		return n.name
	}
	p := d.nodes[n.parent]
	if p == nil {
		return n.name
	}
	return d.pathOfLocked(p) + "/" + n.name
}

// liveLocked reports whether a node is live (no trashed ancestor).
func (d *FSDaemon) liveLocked(n *node) bool {
	for cur := n; cur != nil; {
		if cur.trashed {
			return false
		}
		if cur.parent == "" {
			return true
		}
		cur = d.nodes[cur.parent]
	}
	return false
}

func (d *FSDaemon) liveIndexLocked() map[string]*node {
	idx := map[string]*node{}
	for _, n := range d.nodes {
		if d.liveLocked(n) {
			idx[d.pathOfLocked(n)] = n
		}
	}
	return idx
}

func (d *FSDaemon) ensureFolderLocked(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	idx := d.liveIndexLocked()
	if n, ok := idx[p]; ok {
		return n.id
	}
	parent, name := splitPath(p)
	pid := d.ensureFolderLocked(parent)
	return d.newNodeLocked(pid, name, true, "", "").id
}

func (d *FSDaemon) treeLocked() []wire.FSNode {
	idx := d.liveIndexLocked()
	out := make([]wire.FSNode, 0, len(idx))
	for p, n := range idx {
		out = append(out, wire.FSNode{Path: p, NodeID: n.id, Folder: n.folder, Blob: n.blob, Size: n.size, Doc: n.doc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (d *FSDaemon) trashLocked(recursiveFiles bool) []wire.FSTrashEntry {
	var out []wire.FSTrashEntry
	for _, n := range d.nodes {
		if n.trashed {
			out = append(out, wire.FSTrashEntry{NodeID: n.id, Name: n.name, TrashedAt: n.trashAt, Origin: n.origin, Folder: n.folder})
			if recursiveFiles && n.folder {
				d.collectTrashedFilesLocked(n, n.origin, &out)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Origin < out[j].Origin })
	if out == nil {
		out = []wire.FSTrashEntry{}
	}
	return out
}

func (d *FSDaemon) collectTrashedFilesLocked(root *node, prefix string, out *[]wire.FSTrashEntry) {
	for _, c := range d.nodes {
		if c.parent != root.id || c.trashed {
			continue
		}
		o := prefix + "/" + c.name
		if c.folder {
			d.collectTrashedFilesLocked(c, o, out)
		} else {
			*out = append(*out, wire.FSTrashEntry{NodeID: c.id, Name: c.name, TrashedAt: root.trashAt, Origin: o, Folder: false})
		}
	}
}

func (d *FSDaemon) liveFileCountLocked() int {
	n := 0
	for _, nv := range d.treeLocked() {
		if !nv.Folder {
			n++
		}
	}
	return n
}

func (d *FSDaemon) trashedCountLocked() int {
	n := 0
	for _, x := range d.nodes {
		if x.trashed {
			n++
		}
	}
	return n
}

// wrongMode mirrors requireRealtime's 409 wrong_fs_mode body.
func wrongMode(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, wire.ErrorResponse{Error: "this space is in cas mode; the /v1/fs/* realtime surfaces are not served (set fs_mode=realtime)", Code: "wrong_fs_mode"})
}

// --- handlers --------------------------------------------------------------

func (d *FSDaemon) handleStats(w http.ResponseWriter, r *http.Request, g Grant) {
	d.mu.Lock()
	defer d.mu.Unlock()
	resp := map[string]any{
		"bulk": map[string]any{"policy": wire.BulkPolicy{BulkMinPaths: d.DelMin, BulkFraction: "0.1000", BulkHardPaths: 100, BulkOverwriteMinPaths: 50, BulkOverwriteFraction: "0.5000", BulkOverwriteHardPaths: 500}, "deny": false, "refused": 0},
	}
	if d.HasTree {
		resp["fsmode"] = map[string]any{"mode": d.Mode}
		resp["crdtfs"] = map[string]any{"mode": d.Mode, "nodes": len(d.treeLocked()), "trashed": d.trashedCountLocked()}
	}
	writeJSON(w, 200, resp)
}

func (d *FSDaemon) handleManifest(w http.ResponseWriter, r *http.Request, g Grant) {
	d.mu.Lock()
	defer d.mu.Unlock()
	writeJSON(w, 200, wire.Manifest{Version: d.Version, Entries: d.Manifest})
}

func (d *FSDaemon) handleBlobPut(w http.ResponseWriter, r *http.Request, g Grant) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST")
		return
	}
	var buf strings.Builder
	n, _ := copyBounded(&buf, r)
	data := buf.String()
	h := fmt.Sprintf("%064x", fnv(data))
	d.mu.Lock()
	d.Blobs[h] = []byte(data)
	d.mu.Unlock()
	writeJSON(w, 200, map[string]any{"hash": h, "size": n})
}

func (d *FSDaemon) handleCheckpoints(w http.ResponseWriter, r *http.Request, g Grant) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []wire.Checkpoint{}
	for name, v := range d.Checkpts {
		out = append(out, wire.Checkpoint{Name: name, Version: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, 200, out)
}

// handleSpaceConfig mirrors fsmode.go handleSpaceConfig: GET {fs_mode}; PUT
// (admin) {fs_mode, prefixes?, confirm?} -> doFlip.
func (d *FSDaemon) handleSpaceConfig(w http.ResponseWriter, r *http.Request, g Grant) {
	switch r.Method {
	case http.MethodGet:
		d.mu.Lock()
		defer d.mu.Unlock()
		writeJSON(w, 200, map[string]any{"fs_mode": d.Mode})
	case http.MethodPut:
		var body struct {
			FSMode   string              `json:"fs_mode"`
			Prefixes map[string]string   `json:"prefixes,omitempty"`
			Confirm  *wire.FSModeConfirm `json:"confirm,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad space config: "+err.Error())
			return
		}
		if len(body.Prefixes) > 0 {
			writeJSON(w, 400, wire.ErrorResponse{Error: "per-prefix filesystem mode is not supported in v1 (whole-space only)", Code: "per_prefix_unsupported"})
			return
		}
		d.doFlip(w, g, body.FSMode, body.Confirm)
	default:
		writeErr(w, 405, "GET or PUT")
	}
}

// handleAdminMode mirrors fs.go handleFSMode: POST {mode} -> doFlip with NO confirm.
func (d *FSDaemon) handleAdminMode(w http.ResponseWriter, r *http.Request, g Grant) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST")
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad body: "+err.Error())
		return
	}
	d.doFlip(w, g, body.Mode, nil)
}

// doFlip mirrors fsmode.go doFlip + applyFlip + renderFlip (the edge contract):
// admin gate 403; bad mode 400; same mode 200 no_op; non-empty + no confirm 428
// fs_mode_confirm_required with the demand; confirm mismatch 428
// fs_mode_confirm_mismatch; else seed/snapshot + the mandatory auto pin.
func (d *FSDaemon) doFlip(w http.ResponseWriter, g Grant, targetRaw string, confirm *wire.FSModeConfirm) {
	if g.Role != "admin" {
		writeErr(w, http.StatusForbidden, "setting filesystem mode requires the admin role")
		return
	}
	if targetRaw != "cas" && targetRaw != "realtime" && targetRaw != "" {
		writeErr(w, 400, "fs_mode must be \"cas\" or \"realtime\"")
		return
	}
	target := targetRaw
	if target == "" {
		target = "cas"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.Mode
	if target == cur {
		writeJSON(w, 200, map[string]any{"fs_mode": cur, "no_op": true})
		return
	}
	var entries, trashed uint32
	if cur == "cas" {
		entries = uint32(len(d.Manifest))
	} else {
		entries = uint32(d.liveFileCountLocked())
		trashed = uint32(d.trashedCountLocked())
	}
	empty := entries == 0 && trashed == 0
	if !empty && confirm == nil {
		body := map[string]any{"region": "/", "from": cur, "to": target, "entries": entries}
		if cur == "cas" {
			body["from_version"] = d.Version
			body["leases_held"] = 0
		} else {
			body["trashed"] = trashed
		}
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{
			"error": "flipping a non-empty region changes its guarantees; re-issue with the exact confirm",
			"code":  "fs_mode_confirm_required", "confirm": body})
		return
	}
	if confirm != nil && (confirm.Region != "/" || confirm.From != cur || confirm.To != target || confirm.Entries != entries) {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{
			"error":   "confirm does not match the current scope (re-read and echo exactly)",
			"code":    "fs_mode_confirm_mismatch",
			"confirm": map[string]any{"region": "/", "from": cur, "to": target, "entries": entries}})
		return
	}
	d.HasTree = true
	if cur == "cas" {
		// seed the tree from the manifest; pin V0 for a non-empty region
		out := map[string]any{"fs_mode": "realtime"}
		if entries > 0 {
			ck := "auto/fsmode-cas-to-realtime-v" + strconv.FormatUint(d.Version, 10)
			d.Checkpts[ck] = d.Version
			out["checkpoint"], out["version"] = ck, d.Version
		}
		paths := make([]string, 0, len(d.Manifest))
		for p := range d.Manifest {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			parent, name := splitPath(p)
			pid := d.ensureFolderLocked(parent)
			n := d.newNodeLocked(pid, name, false, d.Manifest[p].Hash, "")
			n.size = d.Manifest[p].Size
		}
		d.Mode = "realtime"
		writeJSON(w, 200, out)
		return
	}
	// realtime -> cas: snapshot live files into V1, pin it, freeze (trash kept)
	out := map[string]any{"fs_mode": "cas"}
	if !(entries == 0 && trashed == 0 && len(d.Manifest) == 0) {
		d.Version++
		man := map[string]wire.ManifestEntry{}
		for _, nv := range d.treeLocked() {
			if !nv.Folder {
				man[nv.Path] = wire.ManifestEntry{Path: nv.Path, Hash: nv.Blob, Size: nv.Size}
			}
		}
		d.Manifest = man
		ck := "auto/fsmode-realtime-to-cas-v" + strconv.FormatUint(d.Version, 10)
		d.Checkpts[ck] = d.Version
		out["checkpoint"], out["version"] = ck, d.Version
	}
	for id, n := range d.nodes { // FreezeToCas: live nodes graduate; trashed subtrees stay
		if d.liveLocked(n) {
			delete(d.nodes, id)
		}
	}
	d.Mode = "cas"
	writeJSON(w, 200, out)
}

func (d *FSDaemon) handleTree(w http.ResponseWriter, r *http.Request, g Grant) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET")
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Mode != "realtime" {
		wrongMode(w)
		return
	}
	writeJSON(w, 200, map[string]any{"nodes": d.treeLocked()})
}

// handleTrash mirrors fs.go handleFSTrash incl. the ?checkpoint= CAS-mode door.
func (d *FSDaemon) handleTrash(w http.ResponseWriter, r *http.Request, g Grant) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET")
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if ck := r.URL.Query().Get("checkpoint"); ck != "" {
		if !d.HasTree || d.Mode == "realtime" {
			writeJSON(w, http.StatusConflict, wire.ErrorResponse{Error: "no retained pre-flip trash (region is not a flipped-to-cas region)", Code: "wrong_fs_mode"})
			return
		}
		if _, has := d.Checkpts[ck]; !has || !strings.HasPrefix(ck, "auto/fsmode-realtime-to-cas-v") {
			writeErr(w, 400, "unknown or non-flip checkpoint")
			return
		}
		writeJSON(w, 200, map[string]any{"trash": d.trashLocked(true)})
		return
	}
	if d.Mode != "realtime" {
		wrongMode(w)
		return
	}
	writeJSON(w, 200, map[string]any{"trash": d.trashLocked(false)})
}

// handleTrashRestore mirrors fsmode.go handleFSTrashRestore.
func (d *FSDaemon) handleTrashRestore(w http.ResponseWriter, r *http.Request, g Grant) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST")
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.HasTree || d.Mode == "realtime" {
		writeJSON(w, http.StatusConflict, wire.ErrorResponse{Error: "no retained pre-flip trash on this surface (region is not a flipped-to-cas region)", Code: "wrong_fs_mode"})
		return
	}
	var body struct {
		NodeID     string `json:"node_id"`
		Checkpoint string `json:"checkpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad restore: "+err.Error())
		return
	}
	if _, has := d.Checkpts[body.Checkpoint]; !has || !strings.HasPrefix(body.Checkpoint, "auto/fsmode-realtime-to-cas-v") {
		writeErr(w, 400, "unknown or non-flip checkpoint")
		return
	}
	n := d.nodes[body.NodeID]
	if n == nil {
		writeErr(w, 400, "no restorable node "+body.NodeID)
		return
	}
	origin := ""
	for _, te := range d.trashLocked(true) {
		if te.NodeID == n.id && !te.Folder {
			origin = te.Origin
		}
	}
	if origin == "" || n.blob == "" {
		writeErr(w, 400, "no restorable node "+body.NodeID)
		return
	}
	d.Version++
	d.Manifest[origin] = wire.ManifestEntry{Path: origin, Hash: n.blob, Size: n.size}
	writeJSON(w, 200, map[string]any{"restored": origin, "version": d.Version})
}

func (d *FSDaemon) handlePrune(w http.ResponseWriter, r *http.Request, g Grant) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST")
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	evicted := 0
	for id, n := range d.nodes {
		if !d.liveLocked(n) {
			delete(d.nodes, id)
			evicted++
		}
	}
	writeJSON(w, 200, map[string]any{"evicted": evicted, "oldest_trashed_at": 0, "history_bytes_over_cap": false, "integrity_anomalies": 0})
}

// handleOps mirrors fs.go handleFSOps: mode gate, batch bounds, per-op
// resolution with the daemon's exact 400 texts, the ext-34 bulk_denied wall on
// the DELETE count, then ONE atomic apply ({applied:n} [+ outstamped]).
func (d *FSDaemon) handleOps(w http.ResponseWriter, r *http.Request, g Grant) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST")
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Mode != "realtime" {
		wrongMode(w)
		return
	}
	var body struct {
		Ops []wire.FSOp `json:"ops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad fs ops: "+err.Error())
		return
	}
	if len(body.Ops) == 0 {
		writeErr(w, 400, "no ops")
		return
	}
	if len(body.Ops) > 4096 {
		writeErr(w, 400, "too many ops in one batch (max 4096)")
		return
	}
	// Resolve + validate against a working copy so a refused batch changes nothing.
	type pending struct {
		apply func()
	}
	var plan []pending
	idx := d.liveIndexLocked()
	deletes := 0
	resolveParent := func(p string) (string, string, bool) {
		parent, name := splitPath(p)
		if name == "" {
			return "", "", false
		}
		if parent == "" {
			return "", name, true
		}
		pn, ok := idx[parent]
		if !ok || !pn.folder {
			return "", "", false
		}
		return pn.id, name, true
	}
	for _, o := range body.Ops {
		o := o
		switch o.Op {
		case "create", "mkdir":
			pid, name, ok := resolveParent(o.Path)
			if !ok {
				writeErr(w, 400, "parent folder not found for "+o.Path)
				return
			}
			n := &node{id: d.mint(), name: name, parent: pid, folder: o.Op == "mkdir", blob: o.Blob, doc: o.Doc}
			if o.Blob != "" {
				n.size = int64(len(d.Blobs[o.Blob]))
			}
			idx[strings.Trim(o.Path, "/")] = n
			plan = append(plan, pending{func() { d.nodes[n.id] = n }})
		case "rename", "move":
			n, ok := idx[strings.Trim(o.Path, "/")]
			if !ok {
				writeErr(w, 400, "path not found: "+o.Path)
				return
			}
			pid, name, ok := resolveParent(o.To)
			if !ok {
				writeErr(w, 400, "destination parent not found for "+o.To)
				return
			}
			from, to := strings.Trim(o.Path, "/"), strings.Trim(o.To, "/")
			moved := map[string]*node{}
			for k, v := range idx {
				if k == from {
					moved[to] = v
				} else if strings.HasPrefix(k, from+"/") {
					moved[to+k[len(from):]] = v
				}
			}
			for k := range idx {
				if k == from || strings.HasPrefix(k, from+"/") {
					delete(idx, k)
				}
			}
			for k, v := range moved {
				idx[k] = v
			}
			plan = append(plan, pending{func() { n.parent, n.name = pid, name }})
		case "delete":
			n, ok := idx[strings.Trim(o.Path, "/")]
			if !ok {
				writeErr(w, 400, "path not found: "+o.Path)
				return
			}
			origin := strings.Trim(o.Path, "/")
			for k := range idx {
				if k == origin || strings.HasPrefix(k, origin+"/") {
					delete(idx, k)
				}
			}
			deletes++
			plan = append(plan, pending{func() { d.clock++; n.trashed, n.origin, n.trashAt = true, origin, d.clock }})
		case "restore":
			n := d.nodes[o.Node]
			if n == nil || !n.trashed {
				writeErr(w, 400, "no restorable node "+o.Node)
				return
			}
			to := o.To
			if to == "" {
				to = n.origin
			}
			pid, name, ok := resolveParent(to)
			if !ok {
				writeErr(w, 400, "restore destination parent not found for "+to)
				return
			}
			idx[strings.Trim(to, "/")] = n
			plan = append(plan, pending{func() { n.trashed, n.origin, n.parent, n.name = false, "", pid, name }})
		default:
			writeErr(w, 400, "unknown op "+o.Op)
			return
		}
	}
	if deletes > 0 && d.DelMin > 0 && uint32(deletes) >= d.DelMin && !g.AllowBulk {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "bulk change denied: this grant may not delete " + strconv.Itoa(deletes) + " nodes in one call; mint it with -allow-bulk or have an admin apply the change",
			"code":  "bulk_denied",
			"bulk":  map[string]any{"deletes": deletes},
		})
		return
	}
	out := map[string]any{"applied": len(body.Ops)}
	if d.Outstamp {
		d.Outstamp = false
		var names []string
		for _, o := range body.Ops {
			names = append(names, o.Op+" "+o.Path)
		}
		out["outstamped"] = names
		out["warning"] = "some ops were out-stamped by an existing placement and had no effect; re-read the tree and retry"
		writeJSON(w, 200, out)
		return
	}
	for _, p := range plan {
		p.apply()
	}
	writeJSON(w, 200, out)
}

// --- tiny helpers (no extra deps) -------------------------------------------

func copyBounded(dst *strings.Builder, r *http.Request) (int64, error) {
	buf := make([]byte, 4096)
	var n int64
	for {
		k, err := r.Body.Read(buf)
		dst.Write(buf[:k])
		n += int64(k)
		if err != nil {
			return n, nil
		}
	}
}

func fnv(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
