package lessons

import (
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestManualDecisionNeverSharedMemoryLessonProvider(t *testing.T) {
	f := writing(t)
	note := uuid.New()
	var user uuid.UUID
	if e := f.env.Owner.QueryRow(t.Context(), `SELECT owner_id FROM card WHERE id=$1`, f.card).Scan(&user); e != nil {
		t.Fatal(e)
	}
	f.exec(`INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,title,context) VALUES($1,$2,$3,$4,'manual_decision','','PRIVATE-ZIRCONIUM title','PRIVATE-ZIRCONIUM context')`, note, f.ws, f.card, user)
	f.exec(`UPDATE card SET state='done' WHERE id=$1`, f.card)
	// Exercise both finished-study collection and a corrupt queued manual trigger.
	f.exec(`INSERT INTO lesson_job(workspace_id,card_id,triggers,due_at) VALUES($1,$2,ARRAY['finished',$3],now())`, f.ws, f.card, "decision:"+note.String())
	f.b.output = Output{Lessons: []Draft{{Kind: "lab", Text: "Malicious proposed private cite", Cites: []string{"decision:" + note.String()}}}}
	f.run()
	if len(f.b.requests) == 0 {
		t.Fatal("lesson provider was not exercised")
	}
	raw, _ := json.Marshal(f.b.requests)
	if strings.Contains(string(raw), "PRIVATE-ZIRCONIUM") || strings.Contains(string(raw), note.String()) {
		t.Fatalf("private note reached lesson provider: %s", raw)
	}
	f.run()
	if f.count(`SELECT count(*) FROM lesson`) != 0 {
		t.Fatal("private citation became a shared lesson")
	}
}
