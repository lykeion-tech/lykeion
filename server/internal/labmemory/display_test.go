package labmemory

import (
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"testing"
)

func TestDisplayLessonExpandsFactsAndWithholdsChangedAnswers(t *testing.T) {
	x := runningTurn(t)
	lesson := uuid.New()
	ref := Ref{"lesson", lesson}.String()
	x.exec("INSERT INTO lesson(id,workspace_id,kind,text,cites,checked_at,model) VALUES($1,$2,'lab','Lesson',$3,now(),'test')", lesson, x.ws, `[{"ref":"record:`+x.record.String()+`","version":"live"}]`)
	x.log(ref, "read")
	if p, e := x.submit(t.Context(), "lesson", ref); e != nil || len(p) != 0 {
		t.Fatal(e, p)
	}
	verify := func(visible bool) {
		t.Helper()
		e := x.runs.Store.InWorkspaceSnapshot(t.Context(), x.ws, x.user, func(tx *store.Tx) error {
			rows, e := tx.Q.ListPrivateLabMessages(t.Context(), db.ListPrivateLabMessagesParams{ChatID: *x.run.LabChatID, PageSize: 1})
			if e != nil {
				return e
			}
			if len(rows) != 1 || rows[0].Kind != "answer" {
				t.Fatalf("missing answer: %v", rows)
			}
			ss, ee, safe, e := NewDisplayReader(tx, x.user, *x.run.LabChatID).Answer(t.Context(), rows[0])
			if e != nil {
				return e
			}
			if safe != visible {
				t.Fatalf("safe = %v", safe)
			}
			if visible {
				if len(ss) != 1 || len(ss[0].Cites) != 1 || len(ss[0].Cites[0].Facts) != 1 || ss[0].Cites[0].Facts[0].Ref != "record:"+x.record.String() || len(ee) != 1 || ee[0].Via != "read" {
					t.Fatalf("lesson: %+v %+v", ss, ee)
				}
			} else if len(ss) != 0 || len(ee) != 0 {
				t.Fatal("stale content escaped")
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	verify(true)
	x.exec("UPDATE lesson SET status='set_aside' WHERE id=$1", lesson)
	verify(false)
}
