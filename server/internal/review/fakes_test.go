package review

import (
	"context"
	"errors"

	"lykeion/server/internal/jev"
	"lykeion/server/internal/sources"
)

type noulCall struct {
	State     string
	Questions map[string]string
}

type choiceCall struct {
	State, Instructions string
	Options             map[string]string
}

// fakeAsker answers every Noul question with p(state, key), and every Choice
// with choice at probability 0.6.
type fakeAsker struct {
	p       func(state, key string) float64
	choice  string
	noul    []noulCall
	choices []choiceCall
}

func (f *fakeAsker) Noul(_ context.Context, state string, qs map[string]string) (map[string]float64, error) {
	f.noul = append(f.noul, noulCall{state, qs})
	out := map[string]float64{}
	for k := range qs {
		out[k] = 0.9
		if f.p != nil {
			out[k] = f.p(state, k)
		}
	}
	return out, nil
}

func (f *fakeAsker) Choice(_ context.Context, state, instructions string, options map[string]string) (jev.Answer, error) {
	f.choices = append(f.choices, choiceCall{state, instructions, options})
	return jev.Answer{Choice: f.choice, Probabilities: map[string]float64{f.choice: 0.6}}, nil
}

// fakeSources resolves the ids it holds; any other id errors.
type fakeSources struct {
	papers, datasets map[string]sources.Hit
	hits             []sources.Hit
	queries          []string
}

func (f *fakeSources) Paper(_ context.Context, id string) (sources.Hit, error) {
	if h, ok := f.papers[id]; ok {
		return h, nil
	}
	return sources.Hit{}, errors.New("not found")
}

func (f *fakeSources) Dataset(_ context.Context, acc string) (sources.Hit, error) {
	if h, ok := f.datasets[acc]; ok {
		return h, nil
	}
	return sources.Hit{}, errors.New("not found")
}

func (f *fakeSources) Search(_ context.Context, q string) ([]sources.Hit, error) {
	f.queries = append(f.queries, q)
	return f.hits, nil
}
