// Package policy implements the deterministic enforcement half of the jev boundary.
// Model classifications enter only as pinned recorded facts; replay never calls a model.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

type Outcome string

const (
	Advance  Outcome = "advance"
	Block    Outcome = "block"
	Route    Outcome = "route"
	Escalate Outcome = "escalate"
)

type Fact struct {
	Key          string `json:"key"`
	Value        bool   `json:"value"`
	ArtifactHash string `json:"artifact_hash"`
}
type Rule struct {
	ID          string   `json:"id"`
	Require     []string `json:"require"`
	Outcome     Outcome  `json:"outcome"`
	Destination string   `json:"destination,omitempty"`
}
type Policy struct {
	Version  string   `json:"version"`
	Rules    []Rule   `json:"rules"`
	Required []string `json:"required"`
}
type Input struct {
	ManifestHash string `json:"manifest_hash"`
	Facts        []Fact `json:"facts"`
}
type Decision struct {
	Outcome     Outcome  `json:"outcome"`
	Destination string   `json:"destination,omitempty"`
	Rules       []string `json:"rules"`
	Missing     []string `json:"missing"`
	ReplayHash  string   `json:"replay_hash"`
}

func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == hex.EncodeToString(b)
}
func validDestination(s string) bool {
	for _, v := range []string{"Backlog", "Grounding", "Hypotheses", "Experimentation", "Validation", "Reporting", "Done"} {
		if s == v {
			return true
		}
	}
	return false
}

// Evaluate evaluates ordered, versioned rules. First matching rule wins; absence
// of required evidence blocks. Invalid policies fail closed, never advance.
func Evaluate(p Policy, in Input) (Decision, error) {
	d := Decision{Outcome: Block, Rules: []string{}, Missing: []string{}}
	if p.Version == "" || !validHash(in.ManifestHash) {
		return d, errors.New("invalid policy version or manifest hash")
	}
	facts := map[string]Fact{}
	for _, f := range in.Facts {
		if f.Key == "" || !validHash(f.ArtifactHash) {
			return d, errors.New("invalid recorded fact")
		}
		if _, ok := facts[f.Key]; ok {
			return d, errors.New("duplicate fact")
		}
		facts[f.Key] = f
	}
	ids := map[string]bool{}
	for _, r := range p.Rules {
		if r.ID == "" || ids[r.ID] || len(r.Require) == 0 {
			return d, errors.New("invalid rule")
		}
		ids[r.ID] = true
		switch r.Outcome {
		case Advance, Block, Escalate:
			if r.Destination != "" {
				return d, errors.New("unexpected destination")
			}
		case Route:
			if !validDestination(r.Destination) {
				return d, errors.New("invalid destination")
			}
		default:
			return d, errors.New("invalid outcome")
		}
	}
	required := map[string]bool{}
	for _, k := range p.Required {
		if k == "" || required[k] {
			return d, errors.New("invalid required fact")
		}
		required[k] = true
		if _, ok := facts[k]; !ok {
			d.Missing = append(d.Missing, k)
		}
	}
	if len(d.Missing) > 0 {
		d.Rules = append(d.Rules, "missing-required-evidence")
	} else {
		for _, r := range p.Rules {
			match := true
			for _, k := range r.Require {
				f, ok := facts[k]
				if !ok || !f.Value {
					match = false
					break
				}
			}
			if match {
				d.Outcome = r.Outcome
				d.Destination = r.Destination
				d.Rules = append(d.Rules, r.ID)
				break
			}
		}
		if len(d.Rules) == 0 {
			d.Rules = append(d.Rules, "no-rule-matched")
		}
	}
	// Canonicalize a copy: evaluation must not mutate caller input or depend on fact order.
	in.Facts = append([]Fact(nil), in.Facts...)
	sort.Slice(in.Facts, func(i, j int) bool { return in.Facts[i].Key < in.Facts[j].Key })
	sort.Strings(d.Missing)
	raw, e := json.Marshal(struct {
		Policy Policy   `json:"policy"`
		Input  Input    `json:"input"`
		Result Decision `json:"result"`
	}{p, in, d})
	if e != nil {
		return d, e
	}
	sum := sha256.Sum256(raw)
	d.ReplayHash = hex.EncodeToString(sum[:])
	return d, nil
}
