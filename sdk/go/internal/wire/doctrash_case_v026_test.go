package wire

import "testing"

// Review P3 #7: on a case-insensitive disk (a macOS server) ".ACP-TRASH/x" is the same folder
// as ".acp-trash/x", so the reserved name holds in any letter case, and only as a first segment.
func TestReservedDocNameIsCaseBlind(t *testing.T) {
	for _, n := range []string{".acp-trash", ".acp-trash/1@1/x", ".ACP-TRASH/1@1/x", ".Acp-Trash", ".acp-TRASH/e1@2/d"} {
		if !IsReservedDocName(n) {
			t.Fatalf("%q must be reserved", n)
		}
	}
	for _, n := range []string{"a/.acp-trash/x", ".acp-trashy", "acp-trash/x", "notes.md"} {
		if IsReservedDocName(n) {
			t.Fatalf("%q is a normal name", n)
		}
	}
}
