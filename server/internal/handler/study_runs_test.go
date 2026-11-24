package handler

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	"lykeion/server/migrations"
)

func TestStudyRunHistoryGroupsParticipantRuns(t *testing.T) {
	l, lead := groundingLab(t)
	var feed struct {
		Items []struct {
			ID, Worker string
			Round      int
		}
	}
	if c := l.s.call(t, "GET", "/api/study-runs", l.jwt, l.ws, nil, &feed); c != http.StatusOK || len(feed.Items) != 1 {
		t.Fatalf("feed %d %+v", c, feed)
	}
	id := feed.Items[0].ID
	var detail struct {
		ID, Worker   string
		Participants []struct{ ID, Name string }
	}
	if c := l.s.call(t, "GET", "/api/study-runs/"+strings.ReplaceAll(id, "~", "/"), l.jwt, l.ws, nil, &detail); c != 200 || len(detail.Participants) != 1 || detail.Participants[0].ID != lead.id || detail.Worker == "" {
		t.Fatalf("detail %d %+v", c, detail)
	}
}

func TestStudyRunRoundKeepsRecordedWorkAndDistinctOutputs(t *testing.T) {
	l, lead := groundingLab(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := l.s.Env.Owner.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	get := func(round int) studyRunDetail {
		t.Helper()
		var detail studyRunDetail
		if c := l.s.call(t, "GET", fmt.Sprintf("/api/study-runs/%s/grounding/%d", l.card.ID, round), l.jwt, l.ws, nil, &detail); c != 200 {
			t.Fatal(c)
		}
		return detail
	}
	before := get(1)
	if before.TeamName == nil || before.Worker == "" || before.Worker != before.Participants[0].Name || before.Status != "running" || before.Progress == "" || before.Outcome != "" {
		t.Fatalf("running: %+v", before)
	}
	exec("UPDATE agent SET name='New agent name' WHERE id=$1", before.Participants[0].AgentID)
	exec("UPDATE field_pack SET name='New team name' WHERE id=$1", l.team)
	if c, out := l.step(t, lead.token, map[string]any{"member": "member-1", "content": "Compute results."}); c != 201 {
		t.Fatal(c, out)
	}
	member := l.claim(t)
	for _, file := range []struct {
		token string
		size  int64
	}{{lead.token, 32}, {member.token, (64 << 10) + 1}} {
		if c, out := l.postOutput(t, file.token, "result.csv", file.size, nil); c != 201 {
			t.Fatal(c, out)
		}
	}
	exec("UPDATE agent_run SET status='completed',completed_at=now() WHERE id IN ($1,$2)", lead.id, member.id)
	l.record(t, lead, 0, "hand_in", "first", map[string]any{"attempt": 1, "hand_in": map[string]any{"answer": "First result", "outputs": []string{"result.csv", "o2", "missing.txt"}}}, &lead.id)
	insertRun := func(round int, role, status string) string {
		t.Helper()
		var id string
		err := l.s.Env.Owner.QueryRow(ctx, `INSERT INTO agent_run(workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,attempt,completed_at,agent_id)
 SELECT workspace_id,card_id,responsible_user_id,board_column,$2,$3,harness,prompt,$4,1,CASE WHEN $4 IN ('completed','failed') THEN now() END,agent_id FROM agent_run WHERE id=$1 RETURNING id`, lead.id, round, role, status).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// An isolated helper is neither a feed row nor a detail.
	insertRun(3, "auditor", "queued")
	other := insertRun(2, "agent", "failed")
	exec("UPDATE agent_run SET failure_reason='timeout' WHERE id=$1", other)
	second := insertRun(1, "leader", "completed")
	l.record(t, teamRun{id: second}, 0, "hand_in", "second", map[string]any{"attempt": 1, "hand_in": map[string]any{"answer": "Follow-up result"}}, &second)
	d := get(1)
	if d.Status != "reviewing" || d.Progress == "" || d.Outcome != "" || d.CompletedAt != nil {
		t.Fatalf("unsettled review %+v", d)
	}
	exec("UPDATE card SET state='done' WHERE id=$1", l.card.ID)
	d = get(1)
	if d.Status != "completed" || d.Outcome != "Follow-up result" || d.Progress != "" || d.Worker != "New agent name" || d.TeamName == nil || *d.TeamName != "New team name" {
		t.Fatalf("settled: %+v", d)
	}
	old := slices.IndexFunc(d.Participants, func(p store.StudyParticipant) bool { return p.ID.String() == lead.id })
	if old < 0 || d.Participants[old].Name != before.Worker || *d.Participants[old].TeamName != *before.TeamName {
		t.Fatalf("lost original identity: %+v", d.Participants)
	}
	if len(d.Outputs) != 4 {
		t.Fatalf("ambiguous and missing declarations: %+v", d.Outputs)
	}
	seen := map[uuid.UUID]bool{}
	otherWS := l.s.workspace(t, l.jwt, "second").ID
	for _, o := range d.Outputs {
		if !o.Available {
			if o.ID != nil || (o.Name != "result.csv" && o.Name != "missing.txt") {
				t.Fatalf("bad declaration %+v", o)
			}
			continue
		}
		if o.ID == nil || seen[*o.ID] {
			t.Fatalf("missing stable identity %+v", o)
		}
		seen[*o.ID] = true
		if o.Key == "o2" && o.RunID.String() != member.id {
			t.Fatalf("lost member output attribution: %+v", o)
		}
		path := fmt.Sprintf("/api/study-runs/%s/grounding/1/outputs/%s", l.card.ID, o.ID)
		var preview struct {
			Supported, Truncated bool
			Text                 string
		}
		wantSize := 32
		if o.Key == "o2" {
			wantSize = 64 << 10
		}
		if c := l.s.call(t, "GET", path, l.jwt, l.ws, nil, &preview); c != 200 || !preview.Supported || preview.Truncated != (o.Key == "o2") || len(preview.Text) != wantSize {
			t.Fatalf("preview status %d, size %d, truncated %v", c, len(preview.Text), preview.Truncated)
		}
		if c := l.s.call(t, "GET", path, l.jwt, otherWS, nil, nil); c != 404 {
			t.Fatal("cross-workspace preview", c)
		}
		if c := l.s.call(t, "GET", strings.Replace(path, "/1/", "/2/", 1), l.jwt, l.ws, nil, nil); c != 404 {
			t.Fatal("round leak", c)
		}
	}
	if len(seen) != 2 {
		t.Fatal("distinct files", seen)
	}
	if d := get(2); d.Status != "failed" || d.Outcome != "timeout" {
		t.Fatalf("timeout: %+v", d)
	}
	exec("UPDATE agent_run SET completed_at=now() WHERE id=$1", lead.id)
	if d := get(1); d.Outcome != "First result" {
		t.Fatalf("latest settled hand-in: %+v", d)
	}
	exec("UPDATE record SET superseded_at=now() WHERE agent_run_id=$1 AND kind='hand_in'", lead.id)
	if d := get(1); d.Outcome != "Follow-up result" {
		t.Fatalf("superseded outcome: %+v", d)
	}
	exec("UPDATE record SET body=jsonb_set(body,'{hand_in}',jsonb_build_object('status','ask','decision',jsonb_build_object('title','Which cohort should we use?'))) WHERE agent_run_id=$1 AND kind='hand_in'", second)
	exec("UPDATE card SET state='blocked' WHERE id=$1", l.card.ID)
	if d := get(1); d.Status != "blocked" || d.Progress != "Which cohort should we use?" || d.Outcome != "" {
		t.Fatalf("pending decision: %+v", d)
	}
	audit := insertRun(1, "auditor", "queued")
	if d := get(1); d.Status != "queued" || d.Outcome != "" || d.Progress == "" {
		t.Fatalf("pending audit: %+v", d)
	}
	exec("UPDATE agent_run SET status='completed',completed_at=now() WHERE id=$1", audit)
	exec("UPDATE card SET state='stopped', board_column='done',ending='stopped',end_reason='Owner stopped the study' WHERE id=$1", l.card.ID)
	if d := get(1); d.Status != "cancelled" || d.Outcome != "Owner stopped the study" {
		t.Fatalf("stopped: %+v", d)
	}
	if c := l.s.call(t, "GET", "/api/study-runs/"+l.card.ID+"/grounding/3", l.jwt, l.ws, nil, nil); c != 404 {
		t.Fatal("helper-only group", c)
	}
}

func TestStudyRunPaginationAndWorkspaceIsolation(t *testing.T) {
	l, lead := groundingLab(t)
	ctx := t.Context()
	var project struct{ ID string }
	if c := l.s.call(t, "POST", "/api/researches", l.jwt, l.ws, map[string]string{"name": "Project"}, &project); c != 201 {
		t.Fatal(c)
	}
	if _, err := l.s.Env.Owner.Exec(ctx, "UPDATE card SET research_id=$2 WHERE id=$1", l.card.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.s.Env.Owner.Exec(ctx, `INSERT INTO agent_run(workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,created_at)
 SELECT workspace_id,card_id,responsible_user_id,board_column,2,role,harness,prompt,created_at FROM agent_run WHERE id=$1`, lead.id); err != nil {
		t.Fatal(err)
	}
	type page struct {
		Items []store.StudyRunRow
		Next  *string `json:"next_cursor"`
	}
	var first, second, empty page
	base := "/api/study-runs?limit=1&project_id=" + project.ID
	if c := l.s.call(t, "GET", base, l.jwt, l.ws, nil, &first); c != 200 || len(first.Items) != 1 || first.Next == nil || first.Items[0].Round != 2 {
		t.Fatal(c, first)
	}
	if c := l.s.call(t, "GET", base+"&cursor="+*first.Next, l.jwt, l.ws, nil, &second); c != 200 || len(second.Items) != 1 || second.Next != nil || second.Items[0].Round != 1 {
		t.Fatal(c, second)
	}
	if c := l.s.call(t, "GET", "/api/study-runs?cursor="+*first.Next, l.jwt, l.ws, nil, nil); c != 400 {
		t.Fatal("wrong filter", c)
	}
	other := l.s.workspace(t, l.jwt, "other").ID
	var studies []mobileStudy
	if c := l.s.call(t, "GET", "/api/mobile/studies", l.jwt, l.ws, nil, &studies); c != 200 || len(studies) != 1 || studies[0].Latest == nil || studies[0].Latest.ID != first.Items[0].ID {
		t.Fatal("latest run across rounds", c, studies)
	}
	if c := l.s.call(t, "GET", "/api/mobile/studies", l.jwt, other, nil, &studies); c != 200 || len(studies) != 0 {
		t.Fatal("studies crossed workspaces", c, studies)
	}
	if c := l.s.call(t, "GET", base, l.jwt, other, nil, &empty); c != 200 || len(empty.Items) != 0 {
		t.Fatal("workspace leak", c, empty)
	}
	if c := l.s.call(t, "GET", "/api/study-runs/"+l.card.ID+"/grounding/1", l.jwt, other, nil, nil); c != 404 {
		t.Fatal("detail leak", c)
	}
	if c := l.s.call(t, "GET", "/api/study-runs?project_id="+uuid.NewString(), l.jwt, l.ws, nil, &empty); c != 200 || len(empty.Items) != 0 {
		t.Fatal("project leak", c, empty)
	}
}

func TestStudyRunPreMigrationAttribution(t *testing.T) {
	l, lead := groundingLab(t)
	for _, suffix := range []string{"down", "up"} {
		sql, err := migrations.FS.ReadFile("0086_study_run_history." + suffix + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.s.Env.Owner.Exec(t.Context(), string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	var d studyRunDetail
	if code := l.s.call(t, "GET", "/api/study-runs/"+l.card.ID+"/grounding/1", l.jwt, l.ws, nil, &d); code != 200 || len(d.Participants) != 1 {
		t.Fatal(code, d)
	}
	p := d.Participants[0]
	if p.ID.String() != lead.id || p.AgentID == nil || p.Name != "" || p.Role != "leader" || d.Worker != "" || d.TeamName != nil {
		t.Fatalf("legacy attribution: %+v", d)
	}
}
