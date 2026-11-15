package service

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestSoloShippedLeaderClaimIncludesMemberSkills(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.T.Blobs = storage.Local{Dir: t.TempDir()}
	zip, p, err := pack.ReadShipped(pack.Shipped(), "Grounding Team")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.T.Blobs.Put(ctx, "pack", bytes.NewReader(zip), int64(len(zip))); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(p.Manifest)
	var a db.Agent
	err = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		row, e := tx.Q.InsertFieldPack(ctx, db.InsertFieldPackParams{WorkspaceID: f.WS, Name: p.Manifest.Name, Version: p.Manifest.Version, Sha256: p.SHA256, Manifest: manifest, BlobKey: "pack", Source: "shipped", Stage: "grounding"})
		if e != nil {
			return e
		}
		a, e = tx.Q.CreateAgent(ctx, db.CreateAgentParams{WorkspaceID: f.WS, Name: "leader", Harness: "claude", OwnerID: f.Owner, Visibility: "workspace"})
		if e != nil {
			return e
		}
		if e = tx.Q.InsertTeamAgent(ctx, db.InsertTeamAgentParams{WorkspaceID: f.WS, PackID: row.ID, Member: p.Manifest.Leader.Name, AgentID: a.ID}); e != nil {
			return e
		}
		_, e = f.T.Enqueue(ctx, tx, f.Owner, NewAgentRun{Card: f.Card, Column: "grounding", Round: 1, Role: "agent", Harness: "claude", Agent: &a.ID, Prompt: "p"})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	library := skill.Service{Store: f.T.Store, Blobs: f.T.Blobs}
	_, err = library.Create(ctx, f.WS, f.Owner, skill.Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: literature-search\ndescription: custom skill\n---\nCUSTOM SHADOW\n")}}, skill.Source{Kind: "local", Location: "custom.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Env.Owner.Exec(ctx, `INSERT INTO agent_capabilities(workspace_id,agent_id,skills,connectors) VALUES($1,$2,'["literature-search"]','[]')`, f.WS, a.ID); err != nil {
		t.Fatal(err)
	}
	claims, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{f.runtime(t, f.Owner, "solo-skills", "claude")}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims %v %v", claims, err)
	}
	found := false
	for _, s := range claims[0].Skills {
		if s.Name == "literature-search" && len(s.Folder) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("member skill missing: %+v", claims[0].Skills)
	}
	for _, sk := range claims[0].Skills {
		if sk.Name != "literature-search" {
			continue
		}
		tr := tar.NewReader(bytes.NewReader(sk.Folder))
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			if h.Name == "SKILL.md" {
				body, _ := io.ReadAll(tr)
				if strings.Contains(string(body), "CUSTOM SHADOW") {
					t.Fatal("custom skill shadowed the shipped member skill")
				}
			}
		}
	}
}
