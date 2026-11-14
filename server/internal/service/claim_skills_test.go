package service

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

// A Team member's run carries the member's own skills, folder and all, for
// the daemon to stage before the CLI starts.
func TestATeamRunIsClaimedWithItsMembersSkills(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	blobs := storage.Local{Dir: t.TempDir()}
	f.T.Blobs = blobs
	zip := testutil.Pack(t, testutil.PackOpts{})
	p, err := pack.Parse(zip)
	if err != nil {
		t.Fatal(err)
	}
	if err := blobs.Put(ctx, "packs/team", bytes.NewReader(zip), int64(len(zip))); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(p.Manifest)
	sum := sha256.Sum256(zip)
	var packID uuid.UUID
	if err := f.Env.Owner.QueryRow(ctx, `INSERT INTO field_pack(workspace_id,name,version,sha256,manifest,blob_key,source,domain,stage) VALUES ($1,'team','1',$2,$3,'packs/team','shipped','','grounding') RETURNING id`, f.WS, hex.EncodeToString(sum[:]), manifest).Scan(&packID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Env.Owner.Exec(ctx, `INSERT INTO card_team(workspace_id,card_id,board_column,pack_id) VALUES ($1,$2,'grounding',$3)`, f.WS, f.Card, packID); err != nil {
		t.Fatal(err)
	}
	err = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		_, err := f.T.Enqueue(ctx, tx, f.Owner, NewAgentRun{Card: f.Card, Column: "grounding", Round: 1, Role: "member-1", Harness: "claude", Prompt: "p", MaxAttempts: 1})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{f.runtime(t, f.Owner, "d", "claude")}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim: %+v %v", claims, err)
	}
	skills := claims[0].Skills
	if len(skills) != 1 || skills[0].Name != "work" {
		t.Fatalf("claimed skills %+v, want the member's skill work", skills)
	}
	tr := tar.NewReader(bytes.NewReader(skills[0].Folder))
	var body []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "SKILL.md" {
			body, _ = io.ReadAll(tr)
		}
	}
	if string(body) != "Do the work." {
		t.Fatalf("the skill's folder holds SKILL.md %q", body)
	}
}
