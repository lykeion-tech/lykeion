// Package doc implements CodeMirror changes using UTF-16 positions.
package doc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const MaxTextBytes = 1 << 20

var ErrInvalid = errors.New("invalid document changes")

type section struct {
	length int
	insert *string
}
type ChangeSet struct{ sections []section }

func Length(s string) int { return len(utf16.Encode([]rune(s))) }
func (c ChangeSet) Len() int {
	n := 0
	for _, s := range c.sections {
		n += s.length
	}
	return n
}
func (c ChangeSet) NewLen() int {
	n := 0
	for _, s := range c.sections {
		if s.insert == nil {
			n += s.length
		} else {
			n += Length(*s.insert)
		}
	}
	return n
}

// validJSONString rejects lone surrogate escapes instead of silently replacing them.
func validJSONString(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		v, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if v >= 0xd800 && v <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			w, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || w < 0xdc00 || w > 0xdfff {
				return false
			}
			i += 6
		} else if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
	}
	return true
}
func Parse(raw json.RawMessage) (ChangeSet, error) {
	var c ChangeSet
	if !validJSONString(raw) || len(raw) > 7*MaxTextBytes {
		return c, ErrInvalid
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || parts == nil {
		return c, ErrInvalid
	}
	oldLen, newLen := 0, 0
	for _, part := range parts {
		var n int
		var ins *string
		if len(part) > 0 && part[0] == '[' {
			var p []json.RawMessage
			if json.Unmarshal(part, &p) != nil || len(p) == 0 || string(p[0]) == "null" || json.Unmarshal(p[0], &n) != nil {
				return ChangeSet{}, ErrInvalid
			}
			lines := make([]string, len(p)-1)
			for i, b := range p[1:] {
				if len(b) == 0 || b[0] != '"' || json.Unmarshal(b, &lines[i]) != nil || strings.ContainsAny(lines[i], "\n\x00") {
					return ChangeSet{}, ErrInvalid
				}
			}
			s := strings.Join(lines, "\n")
			ins = &s
		} else if json.Unmarshal(part, &n) != nil || string(part) == "null" {
			return ChangeSet{}, ErrInvalid
		}
		if n < 0 || n > MaxTextBytes || oldLen > MaxTextBytes-n {
			return ChangeSet{}, ErrInvalid
		}
		oldLen += n
		if ins == nil {
			newLen += n
		} else {
			newLen += Length(*ins)
		}
		c.sections = append(c.sections, section{n, ins})
		if newLen > MaxTextBytes {
			return ChangeSet{}, ErrInvalid
		}
	}
	return c, nil
}
func validUnits(u []uint16) bool {
	for i := 0; i < len(u); i++ {
		if u[i] >= 0xd800 && u[i] <= 0xdbff {
			i++
			if i >= len(u) || u[i] < 0xdc00 || u[i] > 0xdfff {
				return false
			}
		} else if u[i] >= 0xdc00 && u[i] <= 0xdfff {
			return false
		}
	}
	return true
}
func Slice(text string, from, to int) (string, error) {
	u := utf16.Encode([]rune(text))
	if !utf8.ValidString(text) || from < 0 || to < from || to > len(u) || !validUnits(u[:from]) || !validUnits(u[from:to]) {
		return "", ErrInvalid
	}
	return string(utf16.Decode(u[from:to])), nil
}
func (c ChangeSet) Apply(text string) (string, error) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return "", ErrInvalid
	}
	u := utf16.Encode([]rune(text))
	if len(u) != c.Len() {
		return "", fmt.Errorf("%w: length mismatch", ErrInvalid)
	}
	out := make([]uint16, 0, c.NewLen())
	pos := 0
	for _, s := range c.sections {
		if !validUnits(u[pos : pos+s.length]) {
			return "", ErrInvalid
		}
		if s.insert == nil {
			out = append(out, u[pos:pos+s.length]...)
		} else {
			if !utf8.ValidString(*s.insert) {
				return "", ErrInvalid
			}
			out = append(out, utf16.Encode([]rune(*s.insert))...)
		}
		pos += s.length
	}
	if !validUnits(out) {
		return "", ErrInvalid
	}
	result := string(utf16.Decode(out))
	if len(result) > MaxTextBytes || strings.ContainsRune(result, 0) {
		return "", ErrInvalid
	}
	return result, nil
}
func (c ChangeSet) MapPos(pos, assoc int) int {
	a, b := 0, 0
	for _, s := range c.sections {
		end := a + s.length
		if s.insert == nil {
			if end > pos {
				return b + pos - a
			}
			b += s.length
		} else {
			n := Length(*s.insert)
			if end > pos || (end == pos && assoc < 0 && s.length == 0) {
				if pos == a || assoc < 0 {
					return b
				}
				return b + n
			}
			b += n
		}
		a = end
	}
	return b
}
func (c ChangeSet) Touches(from, to int) bool {
	p := 0
	for _, s := range c.sections {
		end := p + s.length
		if s.insert != nil {
			if from == to {
				if p < from && end > from {
					return true
				}
			} else if (s.length == 0 && p > from && p < to) || (p < to && end > from) {
				return true
			}
		}
		p = end
	}
	return false
}
func Replace(from, to int, insert string, docLen int) ChangeSet {
	if from < 0 || to < from || to > docLen {
		panic("invalid replacement range")
	}
	c := ChangeSet{}
	if from == to && insert == "" {
		if docLen > 0 {
			c.sections = append(c.sections, section{length: docLen})
		}
		return c
	}
	if from > 0 {
		c.sections = append(c.sections, section{length: from})
	}
	c.sections = append(c.sections, section{to - from, &insert})
	if to < docLen {
		c.sections = append(c.sections, section{length: docLen - to})
	}
	return c
}
func (c ChangeSet) MarshalJSON() ([]byte, error) {
	parts := []any{}
	for _, s := range c.sections {
		if s.insert == nil {
			parts = append(parts, s.length)
		} else {
			p := []any{s.length}
			if *s.insert != "" {
				for _, l := range strings.Split(*s.insert, "\n") {
					p = append(p, l)
				}
			}
			parts = append(parts, p)
		}
	}
	return json.Marshal(parts)
}

// Compose combines sequential edits without requiring the source text. Its
// section boundaries follow CodeMirror so MapPos retains association semantics.
func (c ChangeSet) Compose(next ChangeSet) (ChangeSet, error) {
	if c.NewLen() != next.Len() {
		return ChangeSet{}, ErrInvalid
	}
	a, b := newSectionCursor(c), newSectionCursor(next)
	type composedSection struct {
		length, inserted int
		text             []uint16
	}
	sections := []composedSection{}
	add := func(length, inserted int, text []uint16, join bool) {
		if length == 0 && inserted <= 0 {
			return
		}
		last := len(sections) - 1
		if last >= 0 && inserted <= 0 && inserted == sections[last].inserted {
			sections[last].length += length
		} else if last >= 0 && length == 0 && sections[last].length == 0 {
			sections[last].inserted += inserted
		} else if join && last >= 0 {
			sections[last].length += length
			sections[last].inserted += inserted
		} else {
			sections = append(sections, composedSection{length: length, inserted: inserted})
		}
		if len(text) > 0 {
			last = len(sections) - 1
			sections[last].text = append(sections[last].text, text...)
		}
	}
	join := false
	for !a.done() || !b.done() {
		if a.inserted == 0 {
			add(a.length, 0, nil, join)
			a.next()
			continue
		}
		if b.length == 0 && !b.done() {
			add(0, b.inserted, b.text, join)
			b.next()
			continue
		}
		if a.done() || b.done() {
			return ChangeSet{}, ErrInvalid
		}
		n := min(a.outputLength(), b.length)
		previous := len(sections)
		if a.inserted < 0 {
			ins := b.inserted
			if ins >= 0 && b.offset > 0 {
				ins = 0
			}
			var text []uint16
			if ins > 0 {
				text = b.text
			}
			add(n, ins, text, join)
		} else if b.inserted < 0 {
			length := a.length
			if a.offset > 0 {
				length = 0
			}
			add(length, n, a.text[a.offset:a.offset+n], join)
		} else {
			length, ins := a.length, b.inserted
			if a.offset > 0 {
				length = 0
			}
			var text []uint16
			if b.offset > 0 {
				ins = 0
			} else {
				text = b.text
			}
			add(length, ins, text, join)
		}
		join = (a.inserted > n || b.inserted >= 0 && b.length > n) && (join || len(sections) > previous)
		a.advanceOutput(n)
		b.advanceInput(n)
	}
	out := ChangeSet{}
	for _, s := range sections {
		if s.inserted < 0 {
			out.sections = append(out.sections, section{length: s.length})
			continue
		}
		if !validUnits(s.text) {
			return ChangeSet{}, ErrInvalid
		}
		text := string(utf16.Decode(s.text))
		out.sections = append(out.sections, section{s.length, &text})
	}
	return out, nil
}

type sectionCursor struct {
	sections                        []section
	index, length, inserted, offset int
	text                            []uint16
}

func newSectionCursor(c ChangeSet) *sectionCursor {
	s := &sectionCursor{sections: c.sections}
	s.next()
	return s
}
func (s *sectionCursor) next() {
	s.offset = 0
	s.text = nil
	if s.index == len(s.sections) {
		s.length = 0
		s.inserted = -2
		return
	}
	p := s.sections[s.index]
	s.index++
	s.length = p.length
	s.inserted = -1
	if p.insert != nil {
		s.text = utf16.Encode([]rune(*p.insert))
		s.inserted = len(s.text)
	}
}
func (s *sectionCursor) done() bool { return s.inserted == -2 }
func (s *sectionCursor) outputLength() int {
	if s.inserted < 0 {
		return s.length
	}
	return s.inserted
}
func (s *sectionCursor) advanceInput(n int) {
	if n == s.length {
		s.next()
	} else {
		s.length -= n
		s.offset += n
	}
}
func (s *sectionCursor) advanceOutput(n int) {
	if s.inserted == -1 {
		s.advanceInput(n)
	} else if n == s.inserted {
		s.next()
	} else {
		s.inserted -= n
		s.offset += n
	}
}
