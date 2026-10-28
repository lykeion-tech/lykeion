package service

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"lykeion/server/internal/middleware"
	"slices"
	"strings"
	"testing"

	"lykeion/server/internal/availability"
	"lykeion/server/internal/science"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestCoresearchClaimCarriesEnabledSkills(t *testing.T) {
	f := setup(t)
	f.T.Blobs = storage.Local{Dir: t.TempDir()}
	library := skill.Service{Store: f.T.Store, Blobs: f.T.Blobs}
	create := func(name string) skill.Detail {
		t.Helper()
		s, err := library.Create(t.Context(), f.WS, f.Owner, skill.Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\ndescription: Test research method\n---\nUse " + name + ".")}}, skill.Source{Kind: "local", Location: name + ".zip"})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	enabled, disabled := create("enabled-method"), create("disabled-method")
	if _, err := availability.Set(t.Context(), f.T.Store, f.WS, f.Owner, "skill", disabled.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	bundle := science.Skills()[0].Name
	hiddenBundle := science.Skills()[1].Name
	if _, err := availability.Set(t.Context(), f.T.Store, f.WS, f.Owner, "skill", "bundle:"+hiddenBundle, 0, false); err != nil {
		t.Fatal(err)
	}
	err := f.T.Store.InWorkspace(t.Context(), f.WS, f.Owner, func(tx *store.Tx) error {
		p, err := tx.Q.CreateCoresearchProject(t.Context(), "Project")
		if err != nil {
			return err
		}
		chat, err := tx.Q.CreateCoresearchChat(t.Context(), db.CreateCoresearchChatParams{WorkspaceID: f.WS, ProjectID: p, OwnerID: f.Owner, Cli: "claude"})
		if err != nil {
			return err
		}
		_, err = tx.Q.EnqueueCoresearchAgentRun(t.Context(), db.EnqueueCoresearchAgentRunParams{WorkspaceID: f.WS, OwnerID: f.Owner, Harness: "claude", Prompt: "research", CoresearchChatID: &chat.ID, ContinuityScope: "coresearch:" + chat.ID.String()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := f.T.Claim(t.Context(), f.Owner, []RuntimeRef{f.runtime(t, f.Owner, "skills", "claude")}, 1, ClaimOptions{CoresearchOnly: true})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim: %+v %v", claims, err)
	}
	c := claims[0]
	names := []string{}
	for _, s := range c.Skills {
		names = append(names, s.Name)
		tr := tar.NewReader(bytes.NewReader(s.Folder))
		found := false
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if h.Name == "SKILL.md" {
				b, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				found = strings.Contains(string(b), "name:")
			}
		}
		if !found {
			t.Fatalf("%s has no skill content", s.Name)
		}
	}
	if !slices.Contains(names, enabled.Name) || !slices.Contains(names, bundle) || slices.Contains(names, disabled.Name) || slices.Contains(names, hiddenBundle) {
		t.Fatalf("wrong enabled skills: %v", names)
	}
	if len(c.MCP) != 0 || len(c.Connectors) != 0 || len(c.WorkspaceConnectors) != 0 || c.CallToken != "" {
		t.Fatalf("Coresearch gained door/MCP authority: %+v", c)
	}
}

func TestGlobalDisabledSkillHidesExistingPin(t *testing.T) {
	f := setup(t)
	f.T.Blobs = storage.Local{Dir: t.TempDir()}
	library := skill.Service{Store: f.T.Store, Blobs: f.T.Blobs}
	d, err := library.Create(t.Context(), f.WS, f.Owner, skill.Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: pinned-method\ndescription: Test method\n---\nCheck.")}}, skill.Source{Kind: "local", Location: "method.zip"})
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 1, "claude")
	claims, err := f.T.Claim(t.Context(), f.Owner, []RuntimeRef{f.runtime(t, f.Owner, "pinned", "claude")}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("%+v %v", claims, err)
	}
	c := claims[0]
	// The normal routing endpoint authenticates only a running attempt.
	if _, err = f.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='running' WHERE id=$1`, c.AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	role := middleware.Role{AgentRunID: c.AgentRun.ID, WorkspaceID: f.WS, CardID: f.Card, Role: c.AgentRun.Role, Column: c.AgentRun.BoardColumn, Round: c.AgentRun.Round, Scopes: Scopes(c.AgentRun.Kind)}
	ctx := skill.WithRunAuthority(t.Context(), role, c.Token)
	scope := skill.Scope{Kind: "card", ID: f.Card}
	ctx, _, err = library.CandidatesForScope(ctx, f.WS, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = library.ResolveForScope(ctx, f.WS, scope, []string{d.Name}); err != nil {
		t.Fatal(err)
	}
	prepared, _, err := library.CandidatesForScope(ctx, f.WS, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = availability.Set(t.Context(), f.T.Store, f.WS, f.Owner, "skill", d.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	if err = library.Delete(t.Context(), f.WS, f.Owner, d.ID, 1); err != nil {
		t.Fatal(err)
	}
	replacement, err := library.Create(t.Context(), f.WS, f.Owner, skill.Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: pinned-method\ndescription: Replacement method\n---\nNew method.")}}, skill.Source{Kind: "local", Location: "replacement.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID == d.ID {
		t.Fatal("fixture reused the disabled skill identity")
	}
	_, candidates, err := library.CandidatesForScope(ctx, f.WS, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range candidates {
		if c.Name == d.Name {
			t.Error("globally disabled pinned skill remains a candidate")
		}
	}
	if _, err = library.ResolveForScope(prepared, f.WS, scope, []string{d.Name}); !errors.Is(err, skill.ErrRoutingChanged) {
		t.Fatalf("disabled pinned snapshot resolved: %v", err)
	}
}
