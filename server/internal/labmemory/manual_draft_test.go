package labmemory

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
	"strings"
	"testing"
)

func runningDraft(t *testing.T) *turn {
	f := setup(t)
	runs := &service.AgentRuns{Store: &store.Store{Pool: f.env.App}, MCPURL: "sources", LabMCPURL: "lab"}
	var chat db.LabChat
	var run db.AgentRun
	e := runs.Store.InWorkspace(t.Context(), f.ws, f.user, func(tx *store.Tx) error {
		var e error
		chat, e = tx.Q.CreateManualDraftChat(t.Context())
		if e != nil {
			return e
		}
		run, e = Enqueue(t.Context(), tx, runs, chat, f.user, `{"user_request":"Draft","current_draft":{"title":"","context":"","deadline":null,"card_id":null}}`, nil, "claude", "")
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	rows, e := runs.Register(t.Context(), f.user, f.ws, "personal", "machine", []string{"claude"}, nil, agent.Platform{OS: "linux"})
	if e != nil {
		t.Fatal(e)
	}
	ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rows[0].ID}
	claims, e := runs.Claim(t.Context(), f.user, []service.RuntimeRef{ref}, 1)
	if e != nil || len(claims) != 1 {
		t.Fatal(e, claims)
	}
	ref.Attempt = claims[0].AgentRun.Attempt
	if _, e = runs.StartLab(t.Context(), f.user, ref, run.ID, LaunchPrompt); e != nil {
		t.Fatal(e)
	}
	return &turn{f, runs, run, ref, handin.HandIns{Runs: runs, Aristotle: Answers{Store: runs.Store}}}
}
func draftRaw(card *string, cites ...string) json.RawMessage {
	statements := []handin.Statement{}
	if len(cites) > 0 {
		statements = append(statements, handin.Statement{Kind: "fact", Text: "Evidence supports this proposal.", Cites: cites})
	}
	raw, _ := json.Marshal(map[string]any{"message": "Proposed note for review", "draft": handin.ManualDraft{Title: "Review evidence", CardID: card}, "statements": statements})
	return raw
}
func TestManualDraftEvidenceAuthorization(t *testing.T) {
	for _, tc := range []string{"valid", "invented", "unauthorized study", "stale", "wrong purpose", "normal rejects draft"} {
		t.Run(tc, func(t *testing.T) {
			x := runningDraft(t)
			ref := Ref{Kind: "record", ID: x.record}.String()
			var raw = json.RawMessage(draftRaw(nil))
			switch tc {
			case "valid":
				x.log(ref, "read")
				raw = draftRaw(nil, ref)
			case "invented":
				raw = draftRaw(nil, ref)
			case "unauthorized study":
				id := uuid.NewString()
				raw = draftRaw(&id)
			case "stale":
				x.log(ref, "read")
				x.exec("UPDATE record SET superseded_at=now() WHERE id=$1", x.record)
				raw = draftRaw(nil, ref)
			case "wrong purpose":
				raw = json.RawMessage(`{"statements":[{"kind":"gap","text":"No evidence","cites":[]}]}`)
			case "normal rejects draft":
				e := x.runs.OnAttempt(t.Context(), x.user, x.ref, x.run.ID, func(tx *store.Tx, r db.AgentRun) error { _, e := (Answers{}).Answer(t.Context(), tx, r, raw); return e })
				if e == nil {
					t.Fatal("normal answer accepted draft run")
				}
				return
			}
			p, e := x.h.Submit(t.Context(), x.user, x.ref, x.run.ID, raw)
			if tc == "valid" {
				if e != nil || len(p) > 0 {
					t.Fatal(e, p)
				}
				if p, e = x.h.Submit(t.Context(), x.user, x.ref, x.run.ID, raw); e != nil || len(p) > 0 {
					t.Fatal("replay", e, p)
				}
				var n int
				if e = x.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM inbox_item WHERE workspace_id=$1 AND kind='manual_decision'", x.ws).Scan(&n); e != nil || n != 0 {
					t.Fatal(e, n)
				}
				x.exec("UPDATE record SET superseded_at=now() WHERE id=$1", x.record)
				e = x.runs.Store.InWorkspace(t.Context(), x.ws, x.user, func(tx *store.Tx) error {
					chat, e := tx.Q.GetPrivateLabChat(t.Context(), *x.run.LabChatID)
					if e != nil {
						return e
					}
					ok, e := DraftReadable(t.Context(), tx, chat)
					if ok {
						t.Error("revoked source retained readable draft")
					}
					return e
				})
				if e != nil {
					t.Fatal(e)
				}
			} else if e != nil || len(p) == 0 {
				t.Fatal(e, p)
			}
		})
	}
}

func TestManualDraftLaunchAndSettings(t *testing.T) {
	x := runningDraft(t)
	if x.run.LabPurpose != ManualDraftPurpose || !strings.Contains(x.run.ContinuityScope, ManualDraftPurpose) || !strings.Contains(x.run.Skill, "only the person") {
		t.Fatal(x.run)
	}
	var e error
	e = x.runs.Store.InWorkspace(context.Background(), x.ws, x.user, func(tx *store.Tx) error { _, e := tx.Q.GetPrivateLabChat(t.Context(), *x.run.LabChatID); return e })
	if e != nil {
		t.Fatal(e)
	}
}

func TestManualDraftCancellationAndMemberRemoval(t *testing.T) {
	for _, action := range []string{"cancel", "remove member", "linked note lost"} {
		t.Run(action, func(t *testing.T) {
			x := runningDraft(t)
			switch action {
			case "cancel":
				if e := x.runs.Cancel(t.Context(), x.ws, x.user, x.run.ID); e != nil {
					t.Fatal(e)
				}
			case "remove member":
				x.exec("DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", x.ws, x.user)
			case "linked note lost":
				note := manualNote(t, x.fixture)
				x.log(note.String(), "read")
				x.exec("DELETE FROM card WHERE id=$1", x.study)
			}
			p, e := x.h.Submit(t.Context(), x.user, x.ref, x.run.ID, draftRaw(nil))
			if e == nil && len(p) == 0 {
				t.Fatal("unavailable turn published a proposal")
			}
			var n int
			if e = x.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM lab_message WHERE agent_run_id=$1 AND proposal IS NOT NULL", x.run.ID).Scan(&n); e != nil || n != 0 {
				t.Fatal(e, n)
			}
			var retained bool
			if e = x.env.Owner.QueryRow(t.Context(), "SELECT draft IS NOT NULL FROM lab_chat WHERE id=$1", *x.run.LabChatID).Scan(&retained); e != nil || !retained {
				t.Fatal("previous draft lost", e)
			}
		})
	}
}

func TestManualDraftRunEventsArePrivate(t *testing.T) {
	x := runningDraft(t)
	if e := x.runs.Cancel(t.Context(), x.ws, x.user, x.run.ID); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := x.env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id=$1 AND payload->>'agent_run_id'=$2 AND recipient_id IS DISTINCT FROM $3`, x.ws, x.run.ID.String(), x.user).Scan(&n); e != nil || n != 0 {
		t.Fatalf("draft run metadata shared: %d %v", n, e)
	}
}

func TestManualDraftRetainsInputStudyDependency(t *testing.T) {
	x := runningDraft(t)
	x.exec("UPDATE lab_message SET context_card_id=$1 WHERE agent_run_id=$2 AND kind='question'", x.study, x.run.ID)
	if p, e := x.h.Submit(t.Context(), x.user, x.ref, x.run.ID, draftRaw(nil)); e != nil || len(p) > 0 {
		t.Fatal(e, p)
	}
	x.exec("DELETE FROM card WHERE id=$1", x.study)
	err := x.runs.Store.InWorkspace(t.Context(), x.ws, x.user, func(tx *store.Tx) error {
		chat, e := tx.Q.GetPrivateLabChat(t.Context(), *x.run.LabChatID)
		if e != nil {
			return e
		}
		ok, e := DraftReadable(t.Context(), tx, chat)
		if ok {
			t.Error("proposal dropped its input study dependency")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestManualDraftDeletedInputStudyCannotLaunchOrPublish(t *testing.T) {
	for _, phase := range []string{"launch", "publish"} {
		t.Run(phase, func(t *testing.T) {
			x := runningDraft(t)
			id := x.study.String()
			body, _ := json.Marshal(DraftTurn{UserRequest: "Private study context", CurrentDraft: handin.ManualDraft{CardID: &id}})
			x.exec("UPDATE lab_message SET body=$1,context_card_id=$2 WHERE agent_run_id=$3 AND kind='question'", string(body), x.study, x.run.ID)
			x.exec("DELETE FROM card WHERE id=$1", x.study)
			if phase == "publish" {
				p, e := x.h.Submit(t.Context(), x.user, x.ref, x.run.ID, draftRaw(nil))
				if e == nil && len(p) == 0 {
					t.Fatal("deleted input context published")
				}
				return
			}
			err := x.runs.Store.InWorkspace(t.Context(), x.ws, x.user, func(tx *store.Tx) error { _, e := LaunchPrompt(t.Context(), tx, x.run); return e })
			if err == nil {
				t.Fatal("deleted input context launched")
			}
		})
	}
}
