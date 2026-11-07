package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/ab0t-com/acp/sdk/go/pkg/wire"
)

// EventHash is the content hash of one event: SHA-256 (hex) of its JSON with object keys in
// order. A follower keeps it with the seq it saw, so that on a new log id it can ask whether
// the event at its position is still that one (D57 Q1): the same event means the same
// history (a restart, a leader change), another one or none means a restore.
//
// It is the TypeScript SDK's eventHash, byte for byte (testdata/event-hash-vectors.json): the
// event as the server sends it, keys sorted by UTF-16 code units, strings and numbers written
// as JavaScript's JSON.stringify writes them.
func EventHash(e wire.Event) string {
	b, _ := json.Marshal(e)
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return ""
	}
	var sb strings.Builder
	canonicalJSON(&sb, v)
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// EventMark is a follower's position in a log: the seq of the event it follows and that
// event's EventHash.
type EventMark struct {
	Seq  uint64
	Hash string
}

func canonicalJSON(sb *strings.Builder, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			jsString(sb, k)
			sb.WriteByte(':')
			canonicalJSON(sb, x[k])
		}
		sb.WriteByte('}')
	case []any:
		sb.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				sb.WriteByte(',')
			}
			canonicalJSON(sb, e)
		}
		sb.WriteByte(']')
	case string:
		jsString(sb, x)
	case float64:
		sb.WriteString(jsNumber(x))
	case bool:
		if x {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	default: // nil
		sb.WriteString("null")
	}
}

// utf16Less is JavaScript's default sort order: by UTF-16 code units.
func utf16Less(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// jsString writes s as JSON.stringify does: only the quote, the backslash and the control
// characters are escaped (\b \f \n \r \t, else \u00xx); everything else is itself.
func jsString(sb *strings.Builder, s string) {
	const hexd = "0123456789abcdef"
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				sb.WriteString(`\u00`)
				sb.WriteByte(hexd[r>>4])
				sb.WriteByte(hexd[r&0xf])
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
}

// jsNumber is JavaScript's Number::toString for a finite number (what JSON.stringify writes).
func jsNumber(f float64) string {
	if f == 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		if f == 0 {
			return "0" // -0 too
		}
		return "null"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // shortest digits: d.ddde±x
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	k, n := len(digits), x+1 // value = 0.digits × 10^n
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	es := "+"
	if n-1 < 0 {
		es = "-"
	}
	ab := n - 1
	if ab < 0 {
		ab = -ab
	}
	if k == 1 {
		return sign + digits + "e" + es + strconv.Itoa(ab)
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + es + strconv.Itoa(ab)
}
