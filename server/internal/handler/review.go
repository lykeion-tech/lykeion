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
		v, err := validationOf(r, tx, ws, id)
		if err != nil {
			return err
		}
		cols[board.StageIndex(board.Validation)].validationView = v
		for _, rec := range recs {
			if i := board.StageIndex(rec.BoardColumn); i >= 0 {
				cols[i].Attempts = append(cols[i].Attempts, rec.Body)
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
	var statuses []db.Record
	v := &validationView{Mode: handin.ModeOf(study), Statuses: []json.RawMessage{}, Objections: []json.RawMessage{}}
	for _, rec := range study {
		if rec.BoardColumn != board.Validation {
			continue
		}
		switch rec.Kind {
		case "validation_status":
			statuses = append(statuses, rec)
		case "objection":
			v.Objections = append(v.Objections, rec.Body)
			var o struct {
				SameFamily bool `json:"same_family"`
			}
			if json.Unmarshal(rec.Body, &o) == nil && o.SameFamily {
				v.SameFamily = true
			}
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
	if round < 0 {
		for _, r := range statuses {
			round = max(round, r.Round)
		}
	}
	for _, r := range statuses {
		if r.Round == round {
			v.Statuses = append(v.Statuses, r.Body)
		}
	}
	return v, nil
}
