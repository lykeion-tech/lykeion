package handler

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// cardReview answers each column's review attempts, oldest first, in board order.
func (h *H) cardReview(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	type column struct {
		Column   string            `json:"column"`
		Attempts []json.RawMessage `json:"attempts"`
		*validationView
	}
	cols := make([]column, len(board.Stages))
	for i, s := range board.Stages {
		cols[i] = column{Column: s, Attempts: []json.RawMessage{}}
	}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		recs, err := tx.Q.CardKindRecords(r.Context(), db.CardKindRecordsParams{WorkspaceID: ws, CardID: id, Kind: "review"})
		// Rounds are the attempt order; created_at can tie inside one transaction.
		sort.SliceStable(recs, func(i, j int) bool { return recs[i].Round < recs[j].Round })
		if err != nil {
			return err
		}
		// Reviews may refer to an earlier attempt's superseded records.
		byKey := make(map[string]db.Record)
		for _, stage := range board.Stages {
			records, err := tx.Q.ListAllRecords(r.Context(), db.ListAllRecordsParams{WorkspaceID: ws, CardID: id, BoardColumn: stage})
			if err != nil {
				return err
			}
			for _, record := range records {
				if record.Key != "" {
					byKey[record.Key] = record
				}
			}
		}
		v, err := validationOf(r, tx, ws, id)
		if err != nil {
			return err
		}
		cols[board.StageIndex(board.Validation)].validationView = v
		for _, rec := range recs {
			if i := board.StageIndex(rec.BoardColumn); i >= 0 {
				var body map[string]any
				if err := json.Unmarshal(rec.Body, &body); err != nil {
					return err
				}
				body["round"] = rec.Round
				body["at"] = rec.CreatedAt
				for _, field := range []string{"items", "missed"} {
					if _, present := body[field]; !present && field == "missed" {
						continue
					}
					items, _ := body[field].([]any)
					if items == nil {
						body[field] = []any{}
					}
					for _, value := range items {
						item, ok := value.(map[string]any)
						if !ok {
							continue
						}
						if item["checks"] == nil {
							item["checks"] = []any{}
						}
						quote, _ := item["label"].(string)
						cites := []string{}
						key, _ := item["key"].(string)
						if record, found := byKey[key]; found {
							var evidence struct {
								Text, Claim, Problem, Source string
								Cites                        []string
							}
							if err := json.Unmarshal(record.Body, &evidence); err != nil {
								return err
							}
							for _, text := range []string{evidence.Text, evidence.Claim, evidence.Problem} {
								if text != "" {
									quote = text
									break
								}
							}
							cites = append(cites, evidence.Cites...)
							if evidence.Source != "" {
								cites = append(cites, evidence.Source)
							}
						}
						item["quote"], item["cites"] = quote, cites
					}
				}
				answer, err := json.Marshal(body)
				if err != nil {
					return err
				}
				cols[i].Attempts = append(cols[i].Attempts, answer)
			}
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"columns": cols})
}

// validationView is what Validation's own records add to its column: how it
// checked, each hypothesis's status, the judged objections and the rerun.
type validationView struct {
	Mode       string            `json:"mode"`
	Statuses   []json.RawMessage `json:"statuses"`
	Objections []json.RawMessage `json:"objections"`
	Rerun      json.RawMessage   `json:"rerun"`
	RouteBack  json.RawMessage   `json:"route_back"`
	SameFamily bool              `json:"same_family"`
	// Objector says whether the newest round's objector handed in, and why
	// not when it did not; null before that round is reviewed.
	Objector json.RawMessage `json:"objector"`
}

func validationOf(r *http.Request, tx *store.Tx, ws, card uuid.UUID) (*validationView, error) {
	var study []db.Record
	for _, s := range board.Stages {
		recs, err := tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: ws, CardID: card, BoardColumn: s})
		if err != nil {
			return nil, err
		}
		study = append(study, recs...)
	}
	var statuses, objections, reviews []db.Record
	v := &validationView{Mode: handin.ModeOf(study), Statuses: []json.RawMessage{}, Objections: []json.RawMessage{}}
	for _, rec := range study {
		if rec.BoardColumn != board.Validation {
			continue
		}
		switch rec.Kind {
		case "validation_status":
			statuses = append(statuses, rec)
		case "objection":
			objections = append(objections, rec)
		case "review":
			reviews = append(reviews, rec)
		case "rerun":
			v.Rerun = rec.Body
		case "route_back":
			v.RouteBack = rec.Body
		}
	}
	// A route-back leaves the earlier round's statuses live: only the newest
	// round with final statuses stands, else the newest round's code statuses.
	final := func(r db.Record) bool {
		var b struct {
			Final bool `json:"final"`
		}
		return json.Unmarshal(r.Body, &b) == nil && b.Final
	}
	var round int32 = -1
	for _, r := range statuses {
		if final(r) && r.Round > round {
			round = r.Round
		}
	}
	haveFinal := round >= 0
	if !haveFinal {
		for _, r := range statuses {
			round = max(round, r.Round)
		}
	}
	for _, r := range statuses {
		if r.Round == round && (!haveFinal || final(r)) {
			v.Statuses = append(v.Statuses, r.Body)
		}
	}
	// The objections and the objector are the same round's.
	var sameFamily struct {
		SameFamily bool `json:"same_family"`
	}
	for _, r := range objections {
		if r.Round == round {
			// Final statuses cite the judged objection's record key. Keep the
			// original body fields alongside it so clients can join that evidence.
			var body map[string]json.RawMessage
			if err := json.Unmarshal(r.Body, &body); err != nil {
				return nil, err
			}
			key, err := json.Marshal(r.Key)
			if err != nil {
				return nil, err
			}
			body["key"] = key
			answer, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			v.Objections = append(v.Objections, answer)
			if json.Unmarshal(r.Body, &sameFamily) == nil && sameFamily.SameFamily {
				v.SameFamily = true
			}
		}
	}
	for _, r := range reviews {
		var b struct {
			Objector json.RawMessage `json:"objector"`
		}
		if r.Round == round && json.Unmarshal(r.Body, &b) == nil && len(b.Objector) > 0 {
			v.Objector = b.Objector
			if json.Unmarshal(b.Objector, &sameFamily) == nil && sameFamily.SameFamily {
				v.SameFamily = true
			}
		}
	}
	return v, nil
}
