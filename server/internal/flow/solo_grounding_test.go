package flow

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"strings"
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

func TestSoloShippedGroundingLeaderGetsMemberGuidance(t *testing.T) {
	l := newLab(t)
	ctx := context.Background()
	blobs := storage.Local{Dir: t.TempDir()}
	l.e.AgentRuns.Blobs = blobs
	l.in(t, func(tx *store.Tx) error { return pack.InstallShipped(ctx, tx, blobs, pack.Shipped()) })
	l.in(t, func(tx *store.Tx) error { return team.InstallLaboratory(ctx, tx) })
	var a db.Agent
	l.in(t, func(tx *store.Tx) error {
		p, err := tx.Q.BuiltInStageTeam(ctx, db.BuiltInStageTeamParams{WorkspaceID: l.ws, Stage: "grounding"})
		if err != nil {
			return err
		}
		a, err = teamAgent(ctx, tx, p.ID, l.owner, "leader")
		return err
	})
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.ShippedSoloTeam(ctx, a.ID, "hypotheses")
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("leader's other stage: %v", err)
		}
		return nil
	})
	c := l.study(t, "grounding", false, &a.ID)
	l.e.Kick(l.ws, c.ID)
	var skill string
	if err := l.env.Owner.QueryRow(ctx, "SELECT skill FROM agent_run WHERE card_id=$1", c.ID).Scan(&skill); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"literature-search", "several distinct queries", "each condition"} {
		if !strings.Contains(skill, want) {
			t.Errorf("missing %q from solo instructions", want)
		}
	}
	for _, unwanted := range []string{"ask_member", "member_result", "leader names", "leader hands you"} {
		if strings.Contains(skill, unwanted) {
			t.Errorf("solo instructions include %q", unwanted)
		}
	}
}

func TestShippedSoloInstructionsHaveNoDelegationTools(t *testing.T) {
	for _, name := range []string{"Grounding Team", "Hypotheses Team", "Experimentation Team", "Validation Team"} {
		t.Run(name, func(t *testing.T) {
			_, p, err := pack.ReadShipped(pack.Shipped(), name)
			if err != nil {
				t.Fatal(err)
			}
			s := soloInstructions(p.Manifest)
			for _, word := range []string{"ask_member", "member_result", "leader names", "leader hands you"} {
				if strings.Contains(s, word) {
					t.Errorf("contains %s", word)
				}
			}
		})
	}
}
