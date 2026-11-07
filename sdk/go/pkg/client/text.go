package client

// Text fields are sent as JSON, and JSON text is UTF-8. encoding/json silently
// replaces every byte sequence that is not valid UTF-8 with U+FFFD, so a mail body,
// an event note, a document value or a path read from a file or a terminal in another
// encoding (Latin-1, UTF-16, a cut-short write) would reach the other side CHANGED,
// with no error anywhere. The SDK refuses such a request before it is sent: a user's
// text is never silently changed. Every JSON request body goes through checkJSONText;
// callers that turn raw bytes into text some other way (a CRDT diff, a JSON decode of
// user input) use CheckText / CheckTextBytes / CheckJSONText first.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrInvalidText is the class of every *InvalidTextError: errors.Is(err, ErrInvalidText).
var ErrInvalidText = errors.New("acp: text is not valid UTF-8")

// InvalidTextError: a text field holds bytes that are not valid UTF-8. Nothing was
// sent. Field names the field ("text", "body.ops[3].v", "state"…).
type InvalidTextError struct {
	Field string
}

func (e *InvalidTextError) Error() string {
	return fmt.Sprintf("acp: %s is not valid UTF-8 text; nothing was sent (sending it would have silently replaced the bad bytes "+
		"with U+FFFD) — convert it to UTF-8 first, or send the bytes as a file/blob", e.Field)
}

func (e *InvalidTextError) Unwrap() error { return ErrInvalidText }

// CheckText returns an *InvalidTextError naming field when s is not valid UTF-8.
func CheckText(field, s string) error {
	if utf8.ValidString(s) {
		return nil
	}
	return &InvalidTextError{Field: field}
}

// CheckTextBytes is CheckText for bytes (a file read, stdin).
func CheckTextBytes(field string, b []byte) error {
	if utf8.Valid(b) {
		return nil
	}
	return &InvalidTextError{Field: field}
}

// CheckJSONText is CheckTextBytes for encoded JSON (a json.RawMessage, a request
// line): it also refuses a string escape that names half a surrogate pair ("\ud800"
// alone) — valid UTF-8 on the wire, but every JSON decoder that produces UTF-8 turns
// it into U+FFFD, so the text would still arrive changed.
func CheckJSONText(field string, raw []byte) error {
	if !utf8.Valid(raw) || !jsonEscapesWellFormed(raw) {
		return &InvalidTextError{Field: field}
	}
	return nil
}

// jsonEscapesWellFormed reports whether every \u escape inside the JSON strings of
// b that names a surrogate is half of a high+low pair. Malformed JSON is left to the
// decoder (true).
func jsonEscapesWellFormed(b []byte) bool {
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if !inStr {
			if c == '"' {
				inStr = true
			}
			continue
		}
		if c == '"' {
			inStr = false
			continue
		}
		if c != '\\' {
			continue
		}
		if i+1 >= len(b) {
			return true
		}
		if b[i+1] != 'u' {
			i++ // a two-byte escape (\" \\ \n …)
			continue
		}
		r, ok := hex4(b, i+2)
		if !ok {
			return true
		}
		i += 5 // the last hex digit
		switch {
		case r >= 0xDC00 && r <= 0xDFFF:
			return false // a low surrogate on its own
		case r >= 0xD800 && r <= 0xDBFF:
			if i+6 < len(b) && b[i+1] == '\\' && b[i+2] == 'u' {
				if lo, ok := hex4(b, i+3); ok && lo >= 0xDC00 && lo <= 0xDFFF {
					i += 6
					continue
				}
			}
			return false // a high surrogate not followed by a low one
		}
	}
	return true
}

func hex4(b []byte, at int) (rune, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	var r rune
	for _, c := range b[at : at+4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// checkJSONText walks a request body before it is marshalled and refuses the first
// string (struct field, map key or value, slice element, json.RawMessage) that is
// not valid UTF-8. []byte other than json.RawMessage is base64 on the wire (exact),
// and is skipped.
func checkJSONText(v any) error {
	return walkText(reflect.ValueOf(v), "body", 0)
}

// walkTextMaxDepth bounds the walk (request bodies are shallow trees; a cycle in a
// caller's value would also make json.Marshal fail).
const walkTextMaxDepth = 64

func walkText(v reflect.Value, field string, depth int) error {
	if !v.IsValid() || depth > walkTextMaxDepth {
		return nil
	}
	if v.Type() == rawMessageType {
		return CheckJSONText(field, v.Bytes())
	}
	switch v.Kind() {
	case reflect.String:
		return CheckText(field, v.String())
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return walkText(v.Elem(), field, depth+1)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			name := sf.Name
			if tag, ok := sf.Tag.Lookup("json"); ok {
				n, _, _ := strings.Cut(tag, ",")
				if n == "-" {
					continue
				}
				if n != "" {
					name = n
				}
			}
			sub := field + "." + name
			if sf.Anonymous {
				sub = field
			}
			if err := walkText(v.Field(i), sub, depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			k := iter.Key()
			if k.Kind() == reflect.String {
				if err := CheckText(field+" (a key)", k.String()); err != nil {
					return err
				}
			}
			if err := walkText(iter.Value(), field+"."+fmt.Sprint(k.Interface()), depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return nil // []byte: base64 on the wire, exact
		}
		for i := 0; i < v.Len(); i++ {
			if err := walkText(v.Index(i), field+"["+strconv.Itoa(i)+"]", depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// marshalBody is json.Marshal of a request body after the text check. Every request
// body in this package is marshalled through it (a marshal error used to be dropped,
// sending an empty body).
func marshalBody(in any) ([]byte, error) {
	if err := checkJSONText(in); err != nil {
		return nil, err
	}
	b, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("acp: encoding the request: %w", err)
	}
	return b, nil
}
