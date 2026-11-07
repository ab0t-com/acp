// Package acpignore is the matcher for ".acpignore", the file that says which paths a folder
// sync leaves alone (ext-43). It only answers "is this path ignored?": what that means for a
// sync (an ignored path is never uploaded, fetched, overwritten or deleted) is the caller's
// job and is specified in ext-43 Section 7.
//
// The syntax is a strict subset of gitignore. A line outside it is an error, never a guess,
// because every client of a space reads the same file and must reach the same answer.
package acpignore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// FileName is the ignore file's name. Only the one at the sync root is policy.
const FileName = ".acpignore"

// MaxSize is the largest ignore file, in bytes.
const MaxSize = 65536

// Error says why a file is invalid and where. Line is 1-based.
type Error struct {
	Line   int
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Reason) }

// Matcher holds a parsed file. A nil Matcher ignores nothing, so a caller with no file needs
// no special case.
type Matcher struct {
	rules []rule
}

type rule struct {
	line     int
	text     string // the line as written
	negated  bool
	dirOnly  bool
	anchored bool
	segs     []segment
}

// segment is one "/"-separated part of a pattern.
type segment struct {
	toks       []token
	doubleStar bool // exactly "**" in an anchored pattern: matches whole segments
}

type token struct {
	r       rune
	literal bool // from a backslash, or an ordinary character; never a wildcard
}

// Load reads the ignore file at the root of a sync folder. It returns a nil Matcher when there
// is none. Anything else that keeps the file from being read is an error: a sync that could
// not read its rules would ignore less than they say, and publish what they keep back.
func Load(root string) (*Matcher, error) {
	p := filepath.Join(root, FileName)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, &Error{Line: 1, Reason: "it is not a regular file"}
	}
	if fi.Size() > MaxSize {
		return nil, &Error{Line: 1, Reason: fmt.Sprintf("the file is larger than %d bytes", MaxSize)}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse reads an ignore file's bytes (ext-43 Sections 4.2 and 5.1). The first thing wrong is
// the error, as an *Error.
func Parse(data []byte) (*Matcher, error) {
	if len(data) > MaxSize {
		return nil, &Error{Line: 1 + bytes.Count(data[:MaxSize], []byte("\n")), Reason: fmt.Sprintf("the file is larger than %d bytes", MaxSize)}
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	m := &Matcher{}
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		n := i + 1
		if !utf8.ValidString(line) {
			return nil, &Error{Line: n, Reason: "not valid UTF-8 text"}
		}
		for _, marker := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
			if strings.HasPrefix(line, marker) {
				return nil, &Error{Line: n, Reason: "an unresolved merge marker; resolve it, then run this again"}
			}
		}
		r, ok, err := parseLine(line)
		if err != nil {
			return nil, &Error{Line: n, Reason: err.Error()}
		}
		if ok {
			r.line, r.text = n, line
			m.rules = append(m.rules, r)
		}
	}
	return m, nil
}

// parseLine follows the numbered steps of ext-43 Section 5.1. ok is false for a line that is
// not a pattern.
func parseLine(line string) (r rule, ok bool, err error) {
	if strings.HasPrefix(line, "#") {
		return r, false, nil
	}
	body := trimTrailingSpaces(line)
	if body == "" {
		return r, false, nil
	}
	if strings.HasPrefix(body, "!") {
		r.negated = true
		body = body[1:]
	}
	var toks []token
	for rs := []rune(body); len(rs) > 0; rs = rs[1:] {
		if rs[0] != '\\' {
			toks = append(toks, token{r: rs[0]})
			continue
		}
		if len(rs) == 1 {
			return r, false, errors.New("a backslash at the end of the line")
		}
		rs = rs[1:]
		toks = append(toks, token{r: rs[0], literal: true})
	}
	isSlash := func(t token) bool { return t.r == '/' && !t.literal }
	if len(toks) > 0 && isSlash(toks[len(toks)-1]) {
		r.dirOnly = true
		toks = toks[:len(toks)-1]
	}
	if len(toks) == 0 {
		return r, false, errors.New("the pattern is empty")
	}
	for _, t := range toks {
		r.anchored = r.anchored || isSlash(t)
		if t.r == '[' && !t.literal {
			return r, false, errors.New(`character classes ("[...]") are not supported`)
		}
	}
	if r.anchored && isSlash(toks[0]) {
		toks = toks[1:]
	}
	cur := []token{}
	flush := func() error {
		s := segment{toks: cur}
		switch segmentText(cur) {
		case "":
			return errors.New(`an empty path segment ("a//b")`)
		case ".", "..":
			return errors.New(`a "." or ".." path segment`)
		}
		s.doubleStar = r.anchored && len(cur) == 2 && !cur[0].literal && !cur[1].literal && cur[0].r == '*' && cur[1].r == '*'
		r.segs = append(r.segs, s)
		cur = []token{}
		return nil
	}
	for _, t := range toks {
		if isSlash(t) {
			if err := flush(); err != nil {
				return r, false, err
			}
			continue
		}
		cur = append(cur, t)
	}
	if err := flush(); err != nil {
		return r, false, err
	}
	return r, true, nil
}

// trimTrailingSpaces removes trailing spaces, keeping one that a backslash escapes.
func trimTrailingSpaces(line string) string {
	body := strings.TrimRight(line, " ")
	if len(body) == len(line) {
		return line
	}
	slashes := len(body) - len(strings.TrimRight(body, `\`))
	if slashes%2 == 1 {
		return body + " "
	}
	return body
}

func segmentText(toks []token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteRune(t.r)
	}
	return b.String()
}

// Empty reports that no pattern is in force.
func (m *Matcher) Empty() bool { return m == nil || len(m.rules) == 0 }

// Patterns returns the pattern lines as written, in file order.
func (m *Matcher) Patterns() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.rules))
	for _, r := range m.rules {
		out = append(out, r.text)
	}
	return out
}

// Ignored reports whether the file at path is ignored: it, or a directory above it, is
// selected. path is in manifest form: relative, "/"-separated.
func (m *Matcher) Ignored(p string) bool { return m.Check(p, false).Ignored }

// IgnoredDir is Ignored for a directory a scan met. A directory reported here is final: a scan
// can skip it without walking it, and nothing under it can be re-included.
func (m *Matcher) IgnoredDir(p string) bool { return m.Check(p, true).Ignored }

// Selected reports whether this candidate alone is selected, with its parents taken as not
// selected: what a scan needs for an entry, since it never walks into a selected directory.
func (m *Matcher) Selected(p string, isDir bool) bool {
	if m.Empty() {
		return false
	}
	r := m.last(strings.Split(p, "/"), isDir, nil)
	return r != nil && !r.negated
}

// Verdict is the answer to "why": whether a path is ignored and which line decided.
type Verdict struct {
	Ignored bool
	By      string // the path the rule selected: the path itself, or the directory above it
	Line    int    // 0 when no pattern matched
	Pattern string
	Negated bool // a negation decided: the path stays in the sync
}

// Check gives the full answer for a path (ext-43 Section 5.4).
func (m *Matcher) Check(p string, isDir bool) Verdict { return m.check(p, isDir, nil) }

// work counts the matcher's inner steps for the work guard (TestIgnoreMatcherPolynomial,
// R-G2 d); nil in use, so the hot path pays one nil check per step. Over its limit it
// stops the match with a panic the test recovers: a matcher that backtracks fails at once,
// never by hanging the run.
type work struct{ n, limit uint64 }

type overBudget struct{}

func (w *work) step() {
	if w != nil {
		w.n++
		if w.n > w.limit {
			panic(overBudget{})
		}
	}
}

func (m *Matcher) check(p string, isDir bool, w *work) Verdict {
	if m.Empty() {
		return Verdict{}
	}
	segs := strings.Split(p, "/")
	for n := 1; n < len(segs); n++ {
		if r := m.last(segs[:n], true, w); r != nil && !r.negated {
			return Verdict{Ignored: true, By: path.Join(segs[:n]...) + "/", Line: r.line, Pattern: r.text}
		}
	}
	r := m.last(segs, isDir, w)
	if r == nil {
		return Verdict{}
	}
	return Verdict{Ignored: !r.negated, By: p, Line: r.line, Pattern: r.text, Negated: r.negated}
}

// last is the last pattern in the file that matches a candidate.
func (m *Matcher) last(cand []string, isDir bool, w *work) *rule {
	for i := len(m.rules) - 1; i >= 0; i-- {
		w.step()
		if m.rules[i].matches(cand, isDir, w) {
			return &m.rules[i]
		}
	}
	return nil
}

func (r *rule) matches(cand []string, isDir bool, w *work) bool {
	if r.dirOnly && !isDir {
		return false
	}
	if !r.anchored {
		return r.segs[0].match(cand[len(cand)-1], w)
	}
	return matchSegments(r.segs, cand, w)
}

// matchSegments is the anchored match, a table over (pattern segment, path segment) kept one
// row at a time, so the cost is pattern x path whatever the number of "**" (ext-43 Section 5.6).
// row[j] says the pattern's segments from i on match the path's segments from j on.
func matchSegments(segs []segment, cand []string, w *work) bool {
	n := len(cand)
	row, next := make([]bool, n+1), make([]bool, n+1)
	row[n] = true
	for i := len(segs) - 1; i >= 0; i-- {
		row, next = next, row
		s := &segs[i]
		switch {
		case s.doubleStar && i == len(segs)-1: // trailing: one or more segments
			for j := 0; j <= n; j++ {
				w.step()
				row[j] = j < n
			}
		case s.doubleStar: // leading or middle: zero or more
			row[n] = next[n]
			for j := n - 1; j >= 0; j-- {
				w.step()
				row[j] = next[j] || row[j+1]
			}
		default:
			row[n] = false
			for j := n - 1; j >= 0; j-- {
				w.step()
				row[j] = next[j+1] && s.match(cand[j], w)
			}
		}
	}
	return row[0]
}

// match compares one segment: the two-pointer method, which on a mismatch only ever moves the
// last "*" on, so the cost is pattern x text.
func (s *segment) match(text string, w *work) bool {
	rs := []rune(text)
	toks := s.toks
	ti, ri, star, mark := 0, 0, -1, 0
	for ri < len(rs) {
		w.step()
		switch {
		case ti < len(toks) && !toks[ti].literal && toks[ti].r == '*':
			star, mark = ti, ri
			ti++
		case ti < len(toks) && (toks[ti].r == rs[ri] || (!toks[ti].literal && toks[ti].r == '?')):
			ti++
			ri++
		case star >= 0:
			mark++
			ti, ri = star+1, mark
		default:
			return false
		}
	}
	for ti < len(toks) && !toks[ti].literal && toks[ti].r == '*' {
		ti++
	}
	return ti == len(toks)
}
