package service

import (
	"encoding/json"
	"testing"

	"lykeion/server/internal/board"
	db "lykeion/server/pkg/db/generated"
)

func TestStandingPreservesExplicitScriptRunSource(t *testing.T) {
	standing := NewStanding([]db.Record{{BoardColumn: board.Experimentation, Kind: "value", Key: "r1", Body: []byte(`{"name":"sum","value":"4","file":"readiness.py","cites":["run:x1"],"script_run":true}`)}})
	if len(standing.Values) != 1 {
		t.Fatalf("standing values: %v", standing.Values)
	}
	var body map[string]any
	if err := json.Unmarshal(standing.Values[0].Body, &body); err != nil || body["script_run"] != true {
		t.Fatalf("explicit script source lost: %s, %v", standing.Values[0].Body, err)
	}
}
