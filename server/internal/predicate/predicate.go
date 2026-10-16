// Package predicate reads predicates over named result keys, such as
// "ci_low > 0.2 and ci_high < 1".
package predicate

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// MaxLen bounds a predicate's text.
const MaxLen = 512

const maxProbes = 1_000_000

var keyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ValidKey reports whether s is a permitted result key.
func ValidKey(s string) bool {
	return keyRE.MatchString(s) && s != "and" && s != "or" && s != "not"
}

// Pred is a parsed boolean expression over finite numeric result values.
type Pred struct {
	op    string
	l, r  *Pred
	key   string
	value float64
}

// Parse parses comparisons joined by and, or, not, and parentheses.
func Parse(s string) (*Pred, error) {
	if len(s) > MaxLen {
		return nil, fmt.Errorf("longer than %d bytes", MaxLen)
	}
	tokens, err := tokenize(s)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.index != len(tokens) {
		return nil, fmt.Errorf("unexpected %q", tokens[p.index])
	}
	return expr, nil
}

func tokenize(s string) ([]string, error) {
	var tokens []string
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(' || c == ')':
			tokens = append(tokens, string(c))
			i++
		case c == '<' || c == '>':
			n := 1
			if i+1 < len(s) && s[i+1] == '=' {
				n = 2
			}
			tokens = append(tokens, s[i:i+n])
			i += n
		default:
			start := i
			for i < len(s) && !strings.ContainsRune(" \t\n\r()<>=", rune(s[i])) {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("unexpected %q: compare with <, <=, > or >=", c)
			}
			tokens = append(tokens, s[start:i])
		}
	}
	return tokens, nil
}

type parser struct {
	tokens []string
	index  int
	depth  int
}

func (p *parser) peek() string {
	if p.index < len(p.tokens) {
		return p.tokens[p.index]
	}
	return ""
}

func (p *parser) next() string {
	token := p.peek()
	if p.index < len(p.tokens) {
		p.index++
	}
	return token
}

func (p *parser) parseOr() (*Pred, error) {
	left, err := p.parseAnd()
	for err == nil && p.peek() == "or" {
		p.next()
		var right *Pred
		right, err = p.parseAnd()
		if err == nil {
			left = &Pred{op: "or", l: left, r: right}
		}
	}
	return left, err
}

func (p *parser) parseAnd() (*Pred, error) {
	left, err := p.parseUnary()
	for err == nil && p.peek() == "and" {
		p.next()
		var right *Pred
		right, err = p.parseUnary()
		if err == nil {
			left = &Pred{op: "and", l: left, r: right}
		}
	}
	return left, err
}

func (p *parser) parseUnary() (*Pred, error) {
	p.depth++
	if p.depth > 32 {
		p.depth--
		return nil, fmt.Errorf("nested more than 32 deep")
	}
	defer func() { p.depth-- }()

	token := p.next()
	switch {
	case token == "not":
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Pred{op: "not", l: inner}, nil
	case token == "(":
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.next() != ")" {
			return nil, fmt.Errorf("missing )")
		}
		return inner, nil
	case token == "":
		return nil, fmt.Errorf("unexpected end")
	case !ValidKey(token):
		return nil, fmt.Errorf("%q is not a result key: lowercase letters, digits and _", token)
	}

	op := p.next()
	if op != "<" && op != "<=" && op != ">" && op != ">=" {
		return nil, fmt.Errorf("%s must be followed by <, <=, > or >=", token)
	}
	number := p.next()
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, fmt.Errorf("%q is not a finite number", number)
	}
	return &Pred{op: op, key: token, value: value}, nil
}

// Eval evaluates p against a result. Every referenced key must be present.
func (p *Pred) Eval(result map[string]float64) (bool, error) {
	switch p.op {
	case "not":
		value, err := p.l.Eval(result)
		return !value, err
	case "and", "or":
		left, err := p.l.Eval(result)
		if err != nil {
			return false, err
		}
		right, err := p.r.Eval(result)
		if err != nil {
			return false, err
		}
		if p.op == "and" {
			return left && right, nil
		}
		return left || right, nil
	}
	value, ok := result[p.key]
	if !ok {
		return false, fmt.Errorf("the result has no %s", p.key)
	}
	switch p.op {
	case "<":
		return value < p.value, nil
	case "<=":
		return value <= p.value, nil
	case ">":
		return value > p.value, nil
	default:
		return value >= p.value, nil
	}
}

func (p *Pred) eachAtom(fn func(*Pred)) {
	if p == nil {
		return
	}
	if p.key != "" {
		fn(p)
	}
	p.l.eachAtom(fn)
	p.r.eachAtom(fn)
}

// Keys returns the referenced result keys in sorted, unique order.
func (p *Pred) Keys() []string {
	keys := make(map[string]struct{})
	p.eachAtom(func(atom *Pred) { keys[atom.key] = struct{}{} })
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// Within builds the inclusive interval predicate low <= key <= high.
func Within(key string, low, high float64) *Pred {
	return &Pred{
		op: "and",
		l:  &Pred{op: ">=", key: key, value: low},
		r:  &Pred{op: "<=", key: key, value: high},
	}
}

// Not negates p.
func Not(p *Pred) *Pred { return &Pred{op: "not", l: p} }

// Joint finds a float64 result satisfying all predicates and the requested
// non-decreasing key order. It searches one representative from each truth
// region induced by every comparison threshold and refuses excessive searches.
func Joint(ordered []string, predicates ...*Pred) (map[string]float64, bool, error) {
	ownThresholds := make(map[string][]float64)
	var orderedThresholds []float64
	for _, predicate := range predicates {
		predicate.eachAtom(func(atom *Pred) {
			ownThresholds[atom.key] = append(ownThresholds[atom.key], atom.value)
			if slices.Contains(ordered, atom.key) {
				orderedThresholds = append(orderedThresholds, atom.value)
			}
		})
	}
	keys := make([]string, 0, len(ownThresholds))
	for key := range ownThresholds {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return nil, false, fmt.Errorf("no comparisons to search")
	}

	probes := make([][]float64, len(keys))
	total := 1
	for i, key := range keys {
		thresholds := ownThresholds[key]
		if slices.Contains(ordered, key) {
			thresholds = orderedThresholds
		}
		probes[i] = probe(thresholds)
		if total > maxProbes/len(probes[i]) {
			return nil, false, fmt.Errorf("too many keys and thresholds to compare exactly")
		}
		total *= len(probes[i])
	}

	indices := make([]int, len(keys))
	point := make(map[string]float64, len(keys))
	for {
		for i, key := range keys {
			point[key] = probes[i][indices[i]]
		}
		if inOrder(point, ordered) && holds(point, predicates) {
			witness := make(map[string]float64, len(point))
			for key, value := range point {
				witness[key] = value
			}
			return witness, true, nil
		}
		index := 0
		for ; index < len(indices); index++ {
			indices[index]++
			if indices[index] < len(probes[index]) {
				break
			}
			indices[index] = 0
		}
		if index == len(indices) {
			return nil, false, nil
		}
	}
}

func probe(thresholds []float64) []float64 {
	sorted := slices.Clone(thresholds)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	values := make([]float64, 0, len(sorted)*3)
	for _, threshold := range sorted {
		values = append(values, threshold)
		below := math.Nextafter(threshold, math.Inf(-1))
		if !math.IsInf(below, 0) {
			values = append(values, below)
		}
		above := math.Nextafter(threshold, math.Inf(1))
		if !math.IsInf(above, 0) {
			values = append(values, above)
		}
	}
	slices.Sort(values)
	return slices.Compact(values)
}

func inOrder(point map[string]float64, ordered []string) bool {
	previous, seen := 0.0, false
	for _, key := range ordered {
		value, ok := point[key]
		if !ok {
			continue
		}
		if seen && value < previous {
			return false
		}
		previous, seen = value, true
	}
	return true
}

func holds(point map[string]float64, predicates []*Pred) bool {
	for _, predicate := range predicates {
		ok, err := predicate.Eval(point)
		if err != nil || !ok {
			return false
		}
	}
	return true
}
