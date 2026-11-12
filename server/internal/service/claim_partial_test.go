package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/skill"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

type failWorkspaceSkillRead struct {
	storage.Storage
	workspace uuid.UUID
}

func (s failWorkspaceSkillRead) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if strings.HasPrefix(key, s.workspace.String()+"/skills/") {
		return nil, errors.New("injected skill storage failure")
	}
	return s.Storage.Get(ctx, key)
}

func TestClaimReturnsOtherPreparedWorkWhenSkillReadFails(t *testing.T) {
	for _, tc := range []struct {
		name     string
		badFirst bool
		onlyBad  bool
	}{{name: "bad-first", badFirst: true}, {name: "bad-last"}, {name: "all-fail", onlyBad: true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			ctx := t.Context()
			badWS := uuid.New()
			err := f.T.Store.InWorkspace(ctx, badWS, f.Owner, func(tx *store.Tx) error {
				if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: badWS, Slug: "s" + badWS.String()[:8], Name: "bad"}); err != nil {
					return err
				}
				return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: badWS, UserID: f.Owner, Role: "owner"})
			})
			if err != nil {
				t.Fatal(err)
			}
			blobs := storage.Local{Dir: t.TempDir()}
			library := skill.Service{Store: f.T.Store, Blobs: blobs}
			_, err = library.Create(ctx, badWS, f.Owner, skill.Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: failing-method\ndescription: Test method\n---\nTest.")}}, skill.Source{Kind: "local", Location: "method.zip"})
			if err != nil {
				t.Fatal(err)
			}
			f.T.Blobs = failWorkspaceSkillRead{Storage: blobs, workspace: badWS}
			makeChat := func(ws uuid.UUID) uuid.UUID {
				t.Helper()
				var run db.AgentRun
				err := f.T.Store.InWorkspace(ctx, ws, f.Owner, func(tx *store.Tx) error {
					p, err := tx.Q.CreateCoresearchProject(ctx, "Project")
					if err != nil {
						return err
					}
					chat, err := tx.Q.CreateCoresearchChat(ctx, db.CreateCoresearchChatParams{WorkspaceID: ws, ProjectID: p, OwnerID: f.Owner, Cli: "claude"})
					if err != nil {
						return err
					}
					run, err = tx.Q.EnqueueCoresearchAgentRun(ctx, db.EnqueueCoresearchAgentRunParams{WorkspaceID: ws, OwnerID: f.Owner, Harness: "claude", Prompt: "research", CoresearchChatID: &chat.ID, ContinuityScope: "coresearch:" + chat.ID.String()})
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				return run.ID
			}
			goodRun, badRun := makeChat(f.WS), makeChat(badWS)
			good := f.runtime(t, f.Owner, "good", "claude")
			runtimes, err := f.T.Register(ctx, f.Owner, badWS, "bad", "laptop", []string{"claude"}, nil, agent.Platform{OS: "linux"})
			if err != nil || len(runtimes) != 1 {
				t.Fatalf("register bad: %+v %v", runtimes, err)
			}
			bad := RuntimeRef{WorkspaceID: badWS, RuntimeID: runtimes[0].ID, Attempt: 1}
			refs := []RuntimeRef{good, bad}
			if tc.badFirst {
				refs = []RuntimeRef{bad, good}
			}
			if tc.onlyBad {
				refs = []RuntimeRef{bad}
			}
			claims, err := f.T.Claim(ctx, f.Owner, refs, 2, ClaimOptions{CoresearchOnly: true})
			if tc.onlyBad {
				if err == nil || len(claims) != 0 {
					t.Fatalf("all failed: returned %+v, %v", claims, err)
				}
				return
			}
			if err != nil || len(claims) != 1 || claims[0].AgentRun.ID != goodRun || claims[0].Token == "" || claims[0].AgentRun.WorkspaceID != f.WS {
				t.Fatalf("want only prepared authorized claim %s, not %s: %+v, %v", goodRun, badRun, claims, err)
			}
			var status string
			if err := f.Env.Owner.QueryRow(ctx, `SELECT status FROM agent_run WHERE id=$1`, badRun).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != "dispatched" {
				t.Fatalf("failed claim status %q, want dispatched for lease recovery", status)
			}
		})
	}
}
