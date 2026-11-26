package report

import (
	"slices"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
)

type approvedReview struct {
	Complete bool          `json:"complete"`
	Decision string        `json:"approval_decision"`
	Checks   review.Result `json:"checks"`
}

// collectMeasurements only uses evidence recorded by an accepted
// Experimentation review. A path in a hand-in is not proof of its producer.
func (b *builder) collectMeasurements() map[string][]Measurement {
	type approval struct {
		round int32
		id    string
	}
	approved := map[approval]bool{}
	for _, r := range b.of(board.Experimentation, "outcome") {
		if r.Role != "server" {
			continue
		}
		outcome := records.Body[struct {
			Verdict  string `json:"verdict"`
			Decision string `json:"decision_id"`
		}](r)
		if outcome.Verdict == "approved_by_owner" && outcome.Decision != "" {
			approved[approval{r.Round, outcome.Decision}] = true
		}
	}
	items := map[string]review.Item{}
	for _, r := range b.of(board.Experimentation, "review", "server_checks") {
		if r.Role != "server" {
			continue
		}
		var result review.Result
		if r.Kind == "review" {
			result = records.Body[review.Result](r)
			if result.Decision != review.DecidePass && result.Decision != review.DecideCarry {
				continue
			}
		} else {
			snapshot := records.Body[approvedReview](r)
			if !snapshot.Complete || !approved[approval{r.Round, snapshot.Decision}] {
				continue
			}
			result = snapshot.Checks
		}
		for _, item := range result.Items {
			if item.Kind == "value" {
				items[item.Key] = item
			}
		}
	}

	b.measurements = map[string]Measurement{}
	byOutput := map[string][]Measurement{}
	for _, r := range b.of(board.Experimentation, "value") {
		item, found := items[r.Key]
		if !found || (item.Status != review.Pass && item.Status != review.Unclear) || item.Measure == nil {
			continue
		}
		if !passedMeasurementCheck(item, "bound") || !passedMeasurementCheck(item, "measure") {
			continue
		}
		body := records.Body[review.ValueBody](r)
		if body.Field == nil || body.Measure == nil || *body.Measure != item.Measure.ID || !item.Measure.Valid() {
			continue
		}
		output, ok := b.byKey[item.Output]
		if !ok || output.Kind != "output" || output.BoardColumn != board.Experimentation ||
			records.Body[struct{ Name string }](output).Name != body.File {
			continue
		}
		m := Measurement{Record: r.Key, Name: body.Name, Value: body.Value, Field: *body.Field,
			Measure: *item.Measure, Output: output.Key}
		b.measurements[r.Key] = m
		byOutput[output.Key] = append(byOutput[output.Key], m)
	}
	return byOutput
}

func passedMeasurementCheck(item review.Item, id string) bool {
	return slices.ContainsFunc(item.Checks, func(c review.Check) bool {
		return c.ID == id && c.By == "server" && c.Status == review.Pass && !c.Warning
	})
}
