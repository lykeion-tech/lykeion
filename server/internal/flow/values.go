package flow

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	db "lykeion/server/pkg/db/generated"
)

// Run is a recorded execution as value matching needs it: its whole output,
// read from the blob when the record's body kept only a cut of it.
// Unreadable is set when that blob is gone, and Stdout then holds only the
// cut the body kept.
type Run struct {
	Tool, Stdout string
	ExitStatus   int
	Unreadable   bool
}

// ValueMatch is one reported value found in a record. Matched is set when a
// cited script run printed it, Exploration when only a cited run_code run
// did; Run names that run. Uncited is set when the value's item cites no
// run. Unchecked names a cited run whose full output could not be read, for a
// value no script run was seen to print.
type ValueMatch struct {
	Record, Value        string
	Run                  string
	Matched, Exploration bool
	Uncited              bool
	Unchecked            string
}

// LoadRuns reads the whole output of each run record in recs, from the blob
// when the body was cut; a blob that is gone marks its run unreadable. It
// does blob I/O, so the caller runs it outside a transaction.
func LoadRuns(ctx context.Context, blobs storage.Storage, recs []db.Record) (map[string]Run, error) {
	runs := map[string]Run{}
	for _, rec := range recs {
		if rec.Kind != "run" {
			continue
		}
		var body struct {
			Tool       string `json:"tool"`
			Output     string `json:"output"`
			ExitStatus int    `json:"exit_status"`
		}
		if json.Unmarshal(rec.Body, &body) != nil {
			runs[rec.Key] = Run{Unreadable: true}
			continue
		}
		run := Run{Tool: body.Tool, Stdout: body.Output, ExitStatus: body.ExitStatus}
		if rec.BlobKey != "" {
			rc, err := blobs.Get(ctx, rec.BlobKey)
			if errors.Is(err, storage.ErrNotFound) {
				run.Unreadable = true
				runs[rec.Key] = run
				continue
			}
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			run.Stdout = string(b)
		}
		runs[rec.Key] = run
	}
	return runs, nil
}

var (
	numberToken = regexp.MustCompile(`-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?`)
	numberText  = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)
)

// maxNumberText bounds a number's text and exponent, so that no printed
// number can cost the scoring pass an enormous exact decimal. ponytail: such
// numbers never match; widen the bounds if real output needs them.
const (
	maxNumberText = 256
	maxExponent   = 9999
)

// number is a number's text read as an exact decimal, with the places it
// states: the digits after its point, less its exponent, never below 0.
// Integer is set for text with neither a point nor an exponent.
type number struct {
	r       *big.Rat
	places  int
	integer bool
}

func parseNumber(s string) (number, bool) {
	if len(s) > maxNumberText || !numberText.MatchString(s) {
		return number{}, false
	}
	mant, exp := s, 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.Atoi(s[i+1:])
		if err != nil || e > maxExponent || e < -maxExponent {
			return number{}, false
		}
		mant, exp = s[:i], e
	}
	frac := 0
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		frac = len(mant) - i - 1
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return number{}, false
	}
	return number{r: r, places: max(frac-exp, 0), integer: !strings.ContainsAny(s, ".eE")}, true
}

// tokens are the numbers printed in out. A minus sign right after a digit is
// not a sign, so `3-4` is 3 and 4, while `a,-0.5` keeps its negative; a comma
// never joins digits, so `1,234` is the numbers 1 and 234.
func tokens(out string) []*big.Rat {
	var rs []*big.Rat
	for _, ix := range numberToken.FindAllStringIndex(out, -1) {
		i := ix[0]
		if out[i] == '-' && i > 0 && out[i-1] >= '0' && out[i-1] <= '9' {
			i++
		}
		if n, ok := parseNumber(out[i:ix[1]]); ok {
			rs = append(rs, n.r)
		}
	}
	return rs
}

// roundTo rounds r to d decimal places, halves away from zero.
func roundTo(r *big.Rat, d int) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d)), nil)
	y := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))
	num := new(big.Int).Abs(y.Num())
	twice := new(big.Int).Lsh(y.Denom(), 1)
	q := new(big.Int).Quo(new(big.Int).Add(new(big.Int).Lsh(num, 1), y.Denom()), twice)
	if y.Sign() < 0 {
		q.Neg(q)
	}
	return new(big.Rat).SetFrac(q, scale)
}

// MatchValues checks each reported value in recs against the runs its record
// cites. A value is a JSON number or a numeric string under a `value` field,
// or in a `values` list, at any depth of a record's body, checked against the
// runs named by the `cites` of the nearest enclosing object that has one.
// A value matches a printed number equal to it once that number is rounded to
// the places the value states; an integer value must be equal exactly.
// Records of the free kinds report no values, and server-written records are
// not the agent's reports; both are skipped.
func MatchValues(recs []db.Record, runs map[string]Run) []ValueMatch {
	// printed holds each run's numbers, rounded once per number of places;
	// places -1 keeps them exact, for integer values.
	type at struct {
		run    string
		places int
	}
	toks := map[string][]*big.Rat{}
	printed := map[at]map[string]bool{}
	prints := func(k string, n number) bool {
		key := at{k, n.places}
		if n.integer {
			key.places = -1
		}
		set, ok := printed[key]
		if !ok {
			t, ok := toks[k]
			if !ok {
				t = tokens(runs[k].Stdout)
				toks[k] = t
			}
			set = map[string]bool{}
			for _, r := range t {
				if key.places >= 0 {
					r = roundTo(r, key.places)
				}
				set[r.RatString()] = true
			}
			printed[key] = set
		}
		return set[n.r.RatString()]
	}
	var out []ValueMatch
	for _, rec := range recs {
		if slices.Contains(service.FreeKinds, rec.Kind) || slices.Contains(service.ServerKinds, rec.Kind) {
			continue
		}
		d := json.NewDecoder(bytes.NewReader(rec.Body))
		d.UseNumber()
		var body any
		if d.Decode(&body) != nil {
			continue
		}
		walkValues(body, nil, func(v string, cites []string) {
			m := ValueMatch{Record: rec.Key, Value: v, Uncited: len(cites) == 0}
			n, ok := parseNumber(v)
			for _, k := range cites {
				r, cited := runs[k]
				if !cited {
					continue
				}
				found := ok && prints(k, n)
				if found && r.Tool == "run_script" {
					m.Matched, m.Exploration, m.Run, m.Unchecked = true, false, k, ""
					break
				}
				if found && !m.Exploration {
					m.Exploration, m.Run = true, k
				}
				if r.Unreadable && (m.Unchecked == "" || r.Tool == "run_script" && runs[m.Unchecked].Tool != "run_script") {
					m.Unchecked = k
				}
			}
			// Once exploration printed the value, an unreadable run_code run
			// can hide nothing more.
			if m.Exploration && runs[m.Unchecked].Tool != "run_script" {
				m.Unchecked = ""
			}
			out = append(out, m)
		})
	}
	slices.SortStableFunc(out, func(a, b ValueMatch) int {
		return cmp.Or(strings.Compare(a.Record, b.Record), strings.Compare(a.Value, b.Value))
	})
	return out
}

// walkValues finds each value as walkQuotes finds quotes, keeping only the
// `run:` entries of a `cites` list.
func walkValues(v any, cites []string, found func(value string, cites []string)) {
	text := func(e any) (string, bool) {
		switch e := e.(type) {
		case json.Number:
			return e.String(), true
		case string:
			s := strings.TrimSpace(e)
			return s, numberText.MatchString(s)
		}
		return "", false
	}
	switch v := v.(type) {
	case []any:
		for _, e := range v {
			walkValues(e, cites, found)
		}
	case map[string]any:
		if list, ok := v["cites"].([]any); ok {
			cites = nil
			for _, c := range list {
				if s, ok := c.(string); ok {
					if k, ok := strings.CutPrefix(s, "run:"); ok {
						cites = append(cites, k)
					}
				}
			}
		}
		for name, e := range v {
			switch name {
			case "value":
				if s, ok := text(e); ok {
					found(s, cites)
					continue
				}
			case "values":
				if list, ok := e.([]any); ok {
					for _, x := range list {
						if s, ok := text(x); ok {
							found(s, cites)
						} else {
							walkValues(x, cites, found)
						}
					}
					continue
				}
			}
			walkValues(e, cites, found)
		}
	}
}
