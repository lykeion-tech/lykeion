package handin

import (
	"encoding/json"
	"slices"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	db "lykeion/server/pkg/db/generated"
)

// AcceptedMeasures resolves unambiguous declarations from the last accepted
// Hypotheses round. The input contains this study's records in creation order.
func AcceptedMeasures(study []db.Record) map[string]review.Measure {
	accepted := map[string]review.Measure{}
	conflicting := map[string]bool{}
	for _, r := range AcceptedHypotheses(study) {
		m := records.Body[review.HypothesisBody](r).Measure
		if m == nil || !m.Valid() || conflicting[m.ID] {
			continue
		}
		if other, ok := accepted[m.ID]; ok && other != *m {
			delete(accepted, m.ID)
			conflicting[m.ID] = true
			continue
		}
		accepted[m.ID] = *m
	}
	return accepted
}

// AcceptedHypotheses returns the live lead declarations designated by the
// latest accepting Hypotheses review or approval. A carried item keeps its
// original record key even when a redo declares the same hypothesis again.
// The input contains this study's records in creation order.
func AcceptedHypotheses(study []db.Record) []db.Record {
	outcome, ok := hypothesisOutcome(study)
	if !ok {
		return nil
	}
	keys := acceptedHypothesisKeys(study, outcome)
	for _, r := range study {
		if r.BoardColumn == board.Hypotheses && r.Kind == review.KindWithdrawn && r.SupersededAt == nil {
			delete(keys, records.Body[review.WithdrawnBody](r).Item)
		}
	}
	var accepted []db.Record
	for _, r := range study {
		if r.BoardColumn == board.Hypotheses && r.Kind == review.KindHypothesis && r.SupersededAt == nil && Leads(r.Role) && keys[r.Key] {
			accepted = append(accepted, r)
		}
	}
	return accepted
}

func hypothesisOutcome(study []db.Record) (db.Record, bool) {
	for _, r := range slices.Backward(study) {
		if r.BoardColumn != board.Hypotheses || r.Kind != "outcome" || r.Role != "server" || r.SupersededAt != nil {
			continue
		}
		switch records.Body[struct{ Verdict string }](r).Verdict {
		case review.DecidePass, review.DecideCarry, "approved_by_owner":
			return r, true
		}
		return db.Record{}, false
	}
	return db.Record{}, false
}

func acceptedHypothesisKeys(study []db.Record, outcome db.Record) map[string]bool {
	decision := records.Body[struct {
		ID string `json:"decision_id"`
	}](outcome).ID
	keys := map[string]bool{}
	for _, r := range slices.Backward(study) {
		if r.BoardColumn != board.Hypotheses || r.Round != outcome.Round || r.Role != "server" || r.SupersededAt != nil {
			continue
		}
		var result review.Result
		switch {
		case decision == "" && r.Kind == "review":
			result = records.Body[review.Result](r)
		case decision != "" && r.Kind == "server_checks":
			snapshot := records.Body[struct {
				Decision string          `json:"approval_decision"`
				Complete bool            `json:"complete"`
				Checks   json.RawMessage `json:"checks"`
			}](r)
			if !snapshot.Complete || snapshot.Decision != decision || json.Unmarshal(snapshot.Checks, &result) != nil {
				continue
			}
		default:
			continue
		}
		for _, item := range result.Items {
			if item.Key != "" && item.Kind == review.KindHypothesis && (item.Status == review.Pass || item.Status == review.Unclear) {
				keys[item.Key] = true
			}
		}
		break
	}
	return keys
}
