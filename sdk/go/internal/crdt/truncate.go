package crdt

// TruncateMinChars is the smallest document (in live characters) the client-side
// truncation guard protects (G0.7 / F-7): a shorter document is cheap to retype, and
// guarding it would nag on trivial edits. It matches the daemon's compaction hold floor.
const TruncateMinChars = 256

// IsTruncation reports whether replacing a document of before live characters with a
// text of after characters deletes more than 90 % of a document of at least
// TruncateMinChars — the shape of an editor crash, an emptied file, a wrong path, or a
// model that sent a partial text. Every client that turns a whole new text into ops
// (the CLI's crdt sync, the MCP bridge's acp_doc_write) asks this one question, so
// the rule cannot drift between them (CL-14 family: a guard present in one client only).
func IsTruncation(before, after int) bool {
	return before >= TruncateMinChars && after*10 < before
}

// UnseenDeleteMaxPercent is the share of a document a write may delete from a text its
// author never saw before that write is refused.
const UnseenDeleteMaxPercent = 10

// DropsUnseenText reports whether a write derived with no view of the document deletes
// more than a small share of its live characters. IsTruncation asks "is this an emptied
// file?", which a real replacement passes: a teammate's short section is under its
// floor, and a draft of the same size is well over its 10 %. Here the writer never saw
// what goes, so there is no floor: every client that can replace a text it never read
// (crdt sync with no sync state, acp_doc_write on first contact) asks this one question.
func DropsUnseenText(before, deleted int) bool {
	return deleted*100 > before*UnseenDeleteMaxPercent
}
