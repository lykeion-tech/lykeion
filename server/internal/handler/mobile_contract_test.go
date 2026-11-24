package handler

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
)

// Keep examples consumed by native contract tests in sync with the Go wire types.
func TestStudyWireFixtures(t *testing.T) {
	for name, wire := range map[string]any{
		"proposal-save-request": new(struct {
			store.ProposalContent
			Revision int64 `json:"revision"`
		}),
		"proposal-start-request": new(struct {
			Revision    int64     `json:"revision"`
			OperationID uuid.UUID `json:"operation_id"`
		}),
		"proposal-start-response": new(proposalReceipt),
		"study-run-page": new(struct {
			Items      []store.StudyRunRow `json:"items"`
			NextCursor *string             `json:"next_cursor"`
		}),
		"study-run-detail": new(studyRunDetail),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/studies/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, wire); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err = json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("fixture differs from wire contract: %s", encoded)
			}
		})
	}
}
