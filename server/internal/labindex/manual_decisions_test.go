package labindex

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/embed"
	"lykeion/server/internal/labmemory"
)

type privateGuardEmbedder struct{ t *testing.T }

func (e privateGuardEmbedder) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	for _, text := range texts {
		if strings.Contains(text, "PRIVATE-ZIRCONIUM") {
			e.t.Fatal("private text sent to shared embedder")
		}
	}
	return (embed.Fake{}).Embed(ctx, texts, query)
}

func TestManualDecisionNeverSharedMemoryIndexer(t *testing.T) {
	t.Setenv("LYK_INDEXER_INTEGRATION", "1")
	x, env, ws, card := indexerFixture(t)
	var user uuid.UUID
	if e := env.Owner.QueryRow(t.Context(), `SELECT owner_id FROM card WHERE id=$1`, card).Scan(&user); e != nil {
		t.Fatal(e)
	}
	id := uuid.New()
	if _, e := env.Owner.Exec(t.Context(), `INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,title,context) VALUES($1,$2,$3,$4,'manual_decision','','PRIVATE-ZIRCONIUM title','PRIVATE-ZIRCONIUM context')`, id, ws, card, user); e != nil {
		t.Fatal(e)
	}
	x.Index.e = privateGuardEmbedder{t}
	if e := drainIndexer(t.Context(), x); e != nil {
		t.Fatal(e)
	}
	if env.Redis.Exists(t.Context(), key(ws, labmemory.Ref{Kind: "decision", ID: id})).Val() != 0 {
		t.Fatal("manual note stored in Redis")
	}
}
