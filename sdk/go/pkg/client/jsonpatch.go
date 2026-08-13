// JSON-Patch (RFC 6902) -> structured-CRDT op mapping (ticket T54).
//
// Consumers whose write path already speaks RFC 6902 (e.g. a liquid-UI
// node.patch/data.patch frame) can feed those patches through this mapper
// and push the result with PushCRDTJSONOps — no rewrite of their op
// surface. The mapping (T54):
//
//	add/replace at an object key     -> set  {path, value}
//	replace at an array index        -> set  {path} (in-place register
//	                                    write; container values expand to
//	                                    ldel+lins, see below)
//	remove at an object key          -> del  {path}
//	remove at an array index         -> ldel {path}
//	add into an array at an index    -> lins {path, idx, value} ("-" = append)
//	test                             -> DROPPED (the CRDT has no baseRev
//	                                    guard — convergence replaces it)
//	move/copy                        -> remove + add (needs the current
//	                                    doc: JSONPatchToOpsWithDoc)
//
// The emitted ops use the client-convenience path form; the daemon
// canonicalizes, stamps, and merges them (ext-5). Push with
// createIntermediate=true to honor RFC 6902 "add" of nested keys.
//
// Caveats (documented, per T54):
//
//   - move maps to a single identity-preserving "mv" op (acp-ext-14): the
//     moved node keeps its nid, so a peer's concurrent edit to it FOLLOWS
//     the move instead of being lost. (The daemon's "crdtjson-move"
//     capability ships alongside "crdtjson".) copy still DUPLICATES —
//     a fresh insert of the copied value — which is correct for copy.
//   - "test" ops are dropped, not evaluated: the CRDT path has no
//     optimistic-locking rev to guard, by design.
//   - Without the current document (JSONPatchToOps), array-vs-object
//     addressing is decided SYNTACTICALLY: a final path segment that is a
//     canonical base-10 integer (or "-") is treated as an array index,
//     anything else as an object key. An object key that LOOKS like an
//     index (e.g. "3") therefore needs the doc-aware variant, which
//     resolves every segment against the real container types.
//   - "replace" maps to set, which also creates a missing key (a
//     superset of RFC 6902 replace, which requires existence).
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ab0t-com/acp/sdk/go/internal/crdtjson"
)

// PatchOp is one RFC 6902 JSON-Patch operation.
type PatchOp struct {
	Op    string          `json:"op"`              // add|replace|remove|move|copy|test
	Path  string          `json:"path"`            // RFC 6901 JSON Pointer
	Value json.RawMessage `json:"value,omitempty"` // add|replace|test
	From  string          `json:"from,omitempty"`  // move|copy
}

// ErrNeedsDoc marks move/copy in the stateless mapper: their source value
// can only be resolved against the current document.
var ErrNeedsDoc = errors.New("move/copy need the current document: use JSONPatchToOpsWithDoc")

// appendIdx is the lins index used for the RFC 6901 "-" (append) segment
// when the array length is unknown; the daemon clamps any past-end index
// to an append.
const appendIdx = math.MaxInt32

// JSONPatchToOps maps an RFC 6902 patch to structured-CRDT ops without
// document state. test ops are dropped; move/copy return ErrNeedsDoc
// (see the package comment for the full mapping and caveats).
func JSONPatchToOps(patch []PatchOp) ([]crdtjson.Op, error) {
	return jsonPatchToOps(patch, nil)
}

// JSONPatchToOpsWithDoc maps an RFC 6902 patch to structured-CRDT ops,
// resolving it against current (the doc's materialized JSON, e.g. from
// CRDTJSONDoc; empty means {}). This variant supports move/copy — their
// source values are read from a local shadow that tracks each op's
// effect, mirroring the daemon's within-request sequencing — and types
// every path segment from the real containers instead of syntactically.
func JSONPatchToOpsWithDoc(patch []PatchOp, current json.RawMessage) ([]crdtjson.Op, error) {
	var shadow any = map[string]any{}
	if len(current) > 0 {
		if err := json.Unmarshal(current, &shadow); err != nil {
			return nil, fmt.Errorf("current doc: %w", err)
		}
	}
	return jsonPatchToOps(patch, &shadow)
}

func jsonPatchToOps(patch []PatchOp, shadow *any) ([]crdtjson.Op, error) {
	var out []crdtjson.Op
	for i, p := range patch {
		ops, err := mapOne(p, shadow)
		if err != nil {
			return nil, fmt.Errorf("patch[%d] (%s %s): %w", i, p.Op, p.Path, err)
		}
		out = append(out, ops...)
	}
	return out, nil
}

func mapOne(p PatchOp, shadow *any) ([]crdtjson.Op, error) {
	switch p.Op {
	case "test":
		return nil, nil // dropped: no baseRev guard on the CRDT path
	case "add", "replace", "remove":
		segs, err := parsePointer(p.Path)
		if err != nil {
			return nil, err
		}
		switch p.Op {
		case "add":
			return mapAdd(segs, p.Value, shadow)
		case "replace":
			return mapReplace(segs, p.Value, shadow)
		default:
			return mapRemove(segs, shadow)
		}
	case "move", "copy":
		if shadow == nil {
			return nil, ErrNeedsDoc
		}
		fromSegs, err := parsePointer(p.From)
		if err != nil {
			return nil, fmt.Errorf("from %q: %w", p.From, err)
		}
		toSegs, err := parsePointer(p.Path)
		if err != nil {
			return nil, err
		}
		val, err := shadowGet(*shadow, fromSegs)
		if err != nil {
			return nil, fmt.Errorf("from %q: %w", p.From, err)
		}
		raw, err := json.Marshal(val)
		if err != nil {
			return nil, err
		}
		if p.Op == "move" {
			// IDENTITY-PRESERVING move -> ONE crdtjson "mv" op (acp-ext-14). The
			// daemon's "crdtjson-move" capability is always on alongside
			// "crdtjson", so a real move is safe to emit; it keeps the moved
			// node's nid, so a concurrent edit to it is not lost (unlike the old
			// remove+add). Update the shadow so later ops in the batch see it.
			from := typedPath(fromSegs, shadow)
			to := typedPath(toSegs, shadow)
			// RFC 6902's move DEST index is interpreted AFTER the source is
			// removed; the crdtjson mv origin is computed against the pre-move
			// array. For a FORWARD move within the SAME array, bump the dest
			// index by one so the element lands at the RFC position once the
			// source vacates (e.g. move /a/0 -> /a/1 on [x,y,z] yields [y,x,z]).
			if fi, ti, same := sameArrayIndexMove(from, to); same && ti > fi {
				to[len(to)-1] = ti + 1
			}
			if err := shadowRemove(shadow, fromSegs); err != nil {
				return nil, fmt.Errorf("from %q: %w", p.From, err)
			}
			if err := shadowAdd(shadow, toSegs, raw); err != nil {
				return nil, err
			}
			return []crdtjson.Op{{T: crdtjson.OpMove, FromPath: from, Path: to}}, nil
		}
		// copy genuinely DUPLICATES: a remove-free add of the fetched value
		// (a fresh nid is correct here).
		add, err := mapAdd(toSegs, raw, shadow)
		if err != nil {
			return nil, err
		}
		return add, nil
	default:
		return nil, fmt.Errorf("unknown patch op %q", p.Op)
	}
}

// sameArrayIndexMove reports whether from and to address two INTEGER indices of
// the SAME array (identical prefix), returning those indices. Used to apply the
// RFC 6902 post-removal dest-index adjustment for a within-array move.
func sameArrayIndexMove(from, to []any) (fi, ti int, ok bool) {
	if len(from) == 0 || len(from) != len(to) {
		return 0, 0, false
	}
	for i := 0; i < len(from)-1; i++ {
		if from[i] != to[i] {
			return 0, 0, false
		}
	}
	fi, fok := from[len(from)-1].(int)
	ti, tok := to[len(to)-1].(int)
	return fi, ti, fok && tok
}

// mapAdd: object key -> set; array index (or "-") -> lins at that index.
func mapAdd(segs []string, val json.RawMessage, shadow *any) ([]crdtjson.Op, error) {
	if len(val) == 0 {
		return nil, errors.New("add requires a value")
	}
	prefix, tail := segs[:len(segs)-1], segs[len(segs)-1]
	isIdx, idx := tailIndex(tail, prefix, shadow, true)
	var ops []crdtjson.Op
	if isIdx {
		i := idx
		ops = []crdtjson.Op{{T: crdtjson.OpLIns, Path: typedPath(prefix, shadow), Idx: &i, Value: val}}
	} else {
		ops = []crdtjson.Op{{T: crdtjson.OpSet, Path: append(typedPath(prefix, shadow), tail), Value: val}}
	}
	if err := shadowAdd(shadow, segs, val); err != nil {
		return nil, err
	}
	return ops, nil
}

// mapReplace: object key or scalar-at-index -> set (in place); a
// CONTAINER value at an array index cannot be written in place (ext-5)
// and expands to ldel+lins at the same position.
func mapReplace(segs []string, val json.RawMessage, shadow *any) ([]crdtjson.Op, error) {
	if len(val) == 0 {
		return nil, errors.New("replace requires a value")
	}
	prefix, tail := segs[:len(segs)-1], segs[len(segs)-1]
	isIdx, idx := tailIndex(tail, prefix, shadow, false)
	if isIdx && idx == appendIdx {
		return nil, errors.New(`"-" is only valid for add`)
	}
	var ops []crdtjson.Op
	if isIdx && isContainer(val) {
		i := idx
		ops = []crdtjson.Op{
			{T: crdtjson.OpLDel, Path: append(typedPath(prefix, shadow), idx)},
			{T: crdtjson.OpLIns, Path: typedPath(prefix, shadow), Idx: &i, Value: val},
		}
	} else {
		p := typedPath(prefix, shadow)
		if isIdx {
			p = append(p, idx)
		} else {
			p = append(p, tail)
		}
		ops = []crdtjson.Op{{T: crdtjson.OpSet, Path: p, Value: val}}
	}
	if err := shadowReplace(shadow, segs, val); err != nil {
		return nil, err
	}
	return ops, nil
}

// mapRemove: object key -> del; array index -> ldel.
func mapRemove(segs []string, shadow *any) ([]crdtjson.Op, error) {
	prefix, tail := segs[:len(segs)-1], segs[len(segs)-1]
	isIdx, idx := tailIndex(tail, prefix, shadow, false)
	if isIdx && idx == appendIdx {
		return nil, errors.New(`"-" is only valid for add`)
	}
	var ops []crdtjson.Op
	if isIdx {
		ops = []crdtjson.Op{{T: crdtjson.OpLDel, Path: append(typedPath(prefix, shadow), idx)}}
	} else {
		ops = []crdtjson.Op{{T: crdtjson.OpDel, Path: append(typedPath(prefix, shadow), tail)}}
	}
	if err := shadowRemove(shadow, segs); err != nil {
		return nil, err
	}
	return ops, nil
}

// --- JSON Pointer (RFC 6901) ---

func parsePointer(p string) ([]string, error) {
	if p == "" {
		return nil, errors.New("empty pointer addresses the whole document — not a patchable target")
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("pointer %q must start with '/'", p)
	}
	segs := strings.Split(p[1:], "/")
	for i, s := range segs {
		s = strings.ReplaceAll(s, "~1", "/")
		segs[i] = strings.ReplaceAll(s, "~0", "~")
	}
	return segs, nil
}

// indexSeg reports whether s is a canonical base-10 array index (RFC 6901:
// no leading zeros, no sign).
func indexSeg(s string) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// tailIndex decides whether the FINAL segment addresses an array index,
// preferring the shadow's real container type over the syntactic form.
// allowAppend admits "-" (add only), reported as appendIdx (or the real
// length when the shadow knows it).
func tailIndex(tail string, prefix []string, shadow *any, allowAppend bool) (bool, int) {
	if shadow != nil {
		if parent, err := shadowGet(*shadow, prefix); err == nil {
			if arr, ok := parent.([]any); ok {
				if tail == "-" && allowAppend {
					return true, len(arr)
				}
				if i, ok := indexSeg(tail); ok {
					return true, i
				}
				return false, 0 // malformed; surfaces in the shadow mutate
			}
			if _, ok := parent.(map[string]any); ok {
				return false, 0
			}
		}
		// prefix not resolvable (e.g. created later server-side):
		// fall through to the syntactic rule.
	}
	if tail == "-" && allowAppend {
		return true, appendIdx
	}
	if i, ok := indexSeg(tail); ok {
		return true, i
	}
	return false, 0
}

// typedPath converts pointer segments to a crdtjson path ([]any of string
// keys and int indexes), using the shadow's container types where it can
// and the syntactic rule where it cannot.
func typedPath(segs []string, shadow *any) []any {
	out := make([]any, 0, len(segs))
	var cur any
	have := false
	if shadow != nil {
		cur, have = *shadow, true
	}
	for _, s := range segs {
		if have {
			switch c := cur.(type) {
			case map[string]any:
				out = append(out, s)
				cur, have = c[s], hasKey(c, s)
				continue
			case []any:
				if i, ok := indexSeg(s); ok {
					out = append(out, i)
					if i >= 0 && i < len(c) {
						cur = c[i]
					} else {
						have = false
					}
					continue
				}
				have = false
			default:
				have = false
			}
		}
		if i, ok := indexSeg(s); ok {
			out = append(out, i)
		} else {
			out = append(out, s)
		}
	}
	return out
}

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func isContainer(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

// --- local shadow (doc-aware variant only; nil shadow = no-op) ---
//
// The shadow mirrors the daemon's within-request sequencing: each mapped
// op is applied to it so later ops (and move/copy sources) resolve
// against the intermediate state, exactly as the daemon's canonicalizer
// applies earlier ops of the same request to its own shadow copy.

func shadowGet(root any, segs []string) (any, error) {
	cur := root
	for _, s := range segs {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[s]
			if !ok {
				return nil, fmt.Errorf("missing key %q", s)
			}
			cur = v
		case []any:
			i, ok := indexSeg(s)
			if !ok || i >= len(c) {
				return nil, fmt.Errorf("no element at %q", s)
			}
			cur = c[i]
		default:
			return nil, fmt.Errorf("segment %q under a scalar", s)
		}
	}
	return cur, nil
}

// shadowMutate walks to the container holding the final segment and calls
// edit(container, tail); edit returns the (possibly re-allocated)
// container, which is written back into the parent.
func shadowMutate(node any, segs []string, edit func(container any, tail string) (any, error)) (any, error) {
	if len(segs) == 1 {
		return edit(node, segs[0])
	}
	switch c := node.(type) {
	case map[string]any:
		child, ok := c[segs[0]]
		if !ok {
			return nil, fmt.Errorf("missing key %q", segs[0])
		}
		nc, err := shadowMutate(child, segs[1:], edit)
		if err != nil {
			return nil, err
		}
		c[segs[0]] = nc
		return c, nil
	case []any:
		i, ok := indexSeg(segs[0])
		if !ok || i >= len(c) {
			return nil, fmt.Errorf("no element at %q", segs[0])
		}
		nc, err := shadowMutate(c[i], segs[1:], edit)
		if err != nil {
			return nil, err
		}
		c[i] = nc
		return c, nil
	default:
		return nil, fmt.Errorf("segment %q under a scalar", segs[0])
	}
}

func decodeVal(raw json.RawMessage) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("value: %w", err)
	}
	return v, nil
}

func shadowAdd(shadow *any, segs []string, raw json.RawMessage) error {
	if shadow == nil {
		return nil
	}
	val, err := decodeVal(raw)
	if err != nil {
		return err
	}
	n, err := shadowMutate(*shadow, segs, func(container any, tail string) (any, error) {
		switch c := container.(type) {
		case map[string]any:
			c[tail] = val
			return c, nil
		case []any:
			i := len(c)
			if tail != "-" {
				var ok bool
				if i, ok = indexSeg(tail); !ok || i > len(c) {
					return nil, fmt.Errorf("bad array index %q", tail)
				}
			}
			c = append(c, nil)
			copy(c[i+1:], c[i:])
			c[i] = val
			return c, nil
		default:
			return nil, fmt.Errorf("add target %q under a scalar", tail)
		}
	})
	if err != nil {
		return err
	}
	*shadow = n
	return nil
}

func shadowReplace(shadow *any, segs []string, raw json.RawMessage) error {
	if shadow == nil {
		return nil
	}
	val, err := decodeVal(raw)
	if err != nil {
		return err
	}
	n, err := shadowMutate(*shadow, segs, func(container any, tail string) (any, error) {
		switch c := container.(type) {
		case map[string]any:
			c[tail] = val // set semantics: creates if missing (documented)
			return c, nil
		case []any:
			i, ok := indexSeg(tail)
			if !ok || i >= len(c) {
				return nil, fmt.Errorf("no element at %q", tail)
			}
			c[i] = val
			return c, nil
		default:
			return nil, fmt.Errorf("replace target %q under a scalar", tail)
		}
	})
	if err != nil {
		return err
	}
	*shadow = n
	return nil
}

func shadowRemove(shadow *any, segs []string) error {
	if shadow == nil {
		return nil
	}
	n, err := shadowMutate(*shadow, segs, func(container any, tail string) (any, error) {
		switch c := container.(type) {
		case map[string]any:
			if !hasKey(c, tail) {
				return nil, fmt.Errorf("missing key %q", tail)
			}
			delete(c, tail)
			return c, nil
		case []any:
			i, ok := indexSeg(tail)
			if !ok || i >= len(c) {
				return nil, fmt.Errorf("no element at %q", tail)
			}
			return append(c[:i], c[i+1:]...), nil
		default:
			return nil, fmt.Errorf("remove target %q under a scalar", tail)
		}
	})
	if err != nil {
		return err
	}
	*shadow = n
	return nil
}
