package wire

import (
	"fmt"
	"strings"
)

// Filter-pattern grammar (ext-1 §4.1, revised 2026-07-12): a pattern is either
// a LITERAL (byte-exact match) or a PREFIX PATTERN — a literal ending in ".*",
// meaning "the literal prefix up to and including the dot, followed by
// anything". So "ladder.*" matches "ladder.gate.failed" but NOT the bare
// "ladder" (list both to cover the parent). No mid-string "*", no "?", no
// regex — a match costs O(len). The daemon's filtered subscription and any
// relay-side re-filtering share THIS matcher, so semantics never diverge.

// UnlabeledPattern is the reserved channel-filter pattern matching ONLY
// unlabeled events (empty channel). It is illegal as a published channel name.
const UnlabeledPattern = "_"

// MatchPattern reports whether s matches pat under the ext-1 §4.1 grammar.
// pat is assumed valid (see ValidPattern); the "_" reservation is a
// channel-filter concern handled by the caller, not here.
func MatchPattern(pat, s string) bool {
	if strings.HasSuffix(pat, ".*") {
		return strings.HasPrefix(s, pat[:len(pat)-1]) // keep the dot: "a.*" ⇒ prefix "a."
	}
	return pat == s
}

// ValidPattern rejects patterns outside the ext-1 §4.1 grammar: empty, or any
// "*" that is not the trailing ".*" form.
func ValidPattern(pat string) error {
	if pat == "" {
		return fmt.Errorf("empty pattern")
	}
	if i := strings.IndexByte(pat, '*'); i >= 0 {
		if i != len(pat)-1 || !strings.HasSuffix(pat, ".*") || pat == ".*" {
			return fmt.Errorf("pattern %q: %q is legal only as a trailing %q", pat, "*", ".*")
		}
	}
	return nil
}

// ValidChannelName enforces ext-1 §4.1 on PUBLISHED channel names:
// [A-Za-z0-9._:-]{1,64}. Empty is legal (unlabeled — the field is optional);
// the bare "_" is reserved for the unlabeled filter pattern and may not be
// published.
func ValidChannelName(s string) error {
	if s == "" {
		return nil
	}
	if s == UnlabeledPattern {
		return fmt.Errorf("channel name %q is reserved (matches only unlabeled events in filters)", UnlabeledPattern)
	}
	if len(s) > 64 {
		return fmt.Errorf("channel name too long: %d bytes (max 64)", len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == ':', c == '-':
		default:
			return fmt.Errorf("channel name %q: illegal byte %q (allowed: [A-Za-z0-9._:-])", s, c)
		}
	}
	return nil
}

// ValidDocName enforces the naming rule for CRDT documents: non-empty, at most
// 256 bytes, no path traversal ("." / ".." / empty segments), no absolute path,
// and none of the characters that are illegal or ambiguous in a file name.
// Document names arrive from clients and become FILE names under the space's
// data dir, so this is a path-safety guard, not a cosmetic one.
//
// It lives here, beside ValidChannelName, because BOTH crdtstore and
// crdtjsonstore need exactly this rule and each used to carry its own
// byte-identical copy. They never disagreed — but a duplicated guard is exactly
// how the write-side blob-hash gap happened (a rule fixed at one call site and
// not at its twin), so the rule now has one definition and one place to fix.
func ValidDocName(name string) error {
	if name == "" {
		return fmt.Errorf("empty document name")
	}
	if len(name) > 256 {
		return fmt.Errorf("document name too long")
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("illegal document name %q", name)
		}
	}
	if strings.ContainsAny(name, "\\:*?\"<>|") || strings.HasPrefix(name, "/") {
		return fmt.Errorf("illegal characters in document name %q", name)
	}
	return nil
}
