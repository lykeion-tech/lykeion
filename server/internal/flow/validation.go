package flow

import (
	"context"

	"github.com/google/uuid"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// otherFamily is the CLI family the objector runs on, given the column's.
var otherFamily = map[string]string{"claude": "codex", "codex": "claude"}

// objectorAssignment is what researchers read the objector was asked to do.
const objectorAssignment = "Look for errors in the study's Validation statuses."

// enqueueObjector queues the blind objector on the card's Validation round
// with brief as its whole prompt: a system run with no agent, the harness's
// default model and no skill. It runs on the other CLI family from the
// column's lead run; when that family has no online runtime and the lead's
// has, it runs on the lead's family, and sameFamily says so. When neither is
// online it waits for the other family like any run. Its session is always
// fresh, so it shares no history with any run.
func (e *Engine) enqueueObjector(ctx context.Context, tx *store.Tx, c db.Card, brief string) (sameFamily bool, err error) {
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return false, err
	}
	lead := ""
	for _, r := range runs {
		if handin.Leads(r.Role) {
			lead = r.Harness
			break
		}
	}
	harness := otherFamily[lead]
	if harness == "" {
		harness = "claude"
	}
	online := func(h string) (bool, error) {
		return tx.Q.HasOnlineRuntime(ctx, db.HasOnlineRuntimeParams{WorkspaceID: tx.WS, OwnerID: c.OwnerID, Harness: h})
	}
	ok, err := online(harness)
	if err != nil {
		return false, err
	}
	if !ok && lead != "" {
		if sameFamily, err = online(lead); err != nil {
			return false, err
		}
		if sameFamily {
			harness = lead
		}
	}
	_, err = e.AgentRuns.Enqueue(ctx, tx, c.OwnerID, service.NewAgentRun{Card: c.ID, Column: c.BoardColumn, Round: c.Round,
		Role: "objector", Harness: harness, Prompt: brief, Assignment: objectorAssignment,
		ContinuityScope: ContinuityScope(c.ID, "objector", c.BoardColumn, c.Round) + "/" + uuid.NewString()})
	return sameFamily, err
}
