package handler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type machineUsageResponse struct {
	Runs []struct {
		RunID        string     `json:"run_id"`
		Colleague    string     `json:"colleague"`
		StudyTitle   string     `json:"study_title"`
		Harness      string     `json:"harness"`
		StartedAt    *time.Time `json:"started_at"`
		Status       string     `json:"status"`
		InputTokens  int64      `json:"input_tokens"`
		OutputTokens int64      `json:"output_tokens"`
		CostUSD      float64    `json:"cost_usd"`
	} `json:"runs"`
	Totals []struct {
		Colleague  string             `json:"colleague"`
		Last7Days  machineUsageWindow `json:"last_7_days"`
		Last30Days machineUsageWindow `json:"last_30_days"`
	} `json:"totals"`
}

type machineUsageWindow struct {
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"cost_usd"`
}

func machineUsageRun(t *testing.T, f runtimeSkillFixture, ws, rt, jwt, title string, started *time.Time) string {
	t.Helper()
	c := f.s.idleCard(t, jwt, ws, "private question sentinel")
	id := uuid.NewString()
	if _, err := f.s.Env.Owner.Exec(t.Context(), `UPDATE card SET title=$1 WHERE id=$2`, title, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,runtime_id,started_at,created_at) VALUES($1,$2,$3,$4,'grounding',1,'leader','claude','private prompt sentinel','completed',$5,$6,COALESCE($6,now()))`, id, ws, c.ID, f.s.userID(t, jwt), rt, started); err != nil {
		t.Fatal(err)
	}
	machineUsageAttempt(t, f, ws, id, 1)
	return id
}

// Synthetic analytics fixtures explicitly model accepted execution provenance.
func machineUsageAttempt(t *testing.T, f runtimeSkillFixture, ws, run string, attempt int) {
	t.Helper()
	if _, err := f.s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_machine_attempt(workspace_id,agent_run_id,attempt,runtime_id,daemon_id,owner_id,responsible_user_id,accepted_at,started_at)
      SELECT r.workspace_id,r.id,$3,rt.id,rt.daemon_id,rt.owner_id,r.responsible_user_id,r.created_at,r.started_at FROM agent_run r JOIN runtime rt ON rt.id=r.runtime_id AND rt.workspace_id=r.workspace_id WHERE r.workspace_id=$1 AND r.id=$2 ON CONFLICT DO NOTHING`, ws, run, attempt); err != nil {
		t.Fatal(err)
	}
}

func machineUsageRow(t *testing.T, f runtimeSkillFixture, ws, run string, attempt int, at time.Time, input, output, read, write int64, cost float64) {
	t.Helper()
	machineUsageAttempt(t, f, ws, run, attempt)
	if _, err := f.s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_usage(workspace_id,agent_run_id,attempt,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd,created_at) VALUES($1,$2,$3,'model',$4,$5,$6,$7,$8,$9)`, ws, run, attempt, input, output, read, write, cost, at); err != nil {
		t.Fatal(err)
	}
}

func machineUsageRead(t *testing.T, f runtimeSkillFixture, ws string) machineUsageResponse {
	t.Helper()
	var got machineUsageResponse
	if code := f.s.call(t, "GET", "/api/machines/skills-daemon/colleagues", f.jwt, ws, nil, &got); code != 200 {
		t.Fatalf("machine colleagues: %d", code)
	}
	return got
}

// Catches including the owner's runs, missing harnesses, and collapsing retry/model usage.
func TestTheOwnerSeesColleaguesRunsAndUsage(t *testing.T) {
	f := runtimeSkillSetup(t)
	a := f.s.member(t, f.jwt, f.ws, "usage-a@lab.org", "member")
	b := f.s.member(t, f.jwt, f.ws, "usage-b@lab.org", "member")
	if _, err := f.s.Env.Owner.Exec(t.Context(), `UPDATE "user" SET name='Ada' WHERE id=$1`, f.s.userID(t, a)); err != nil {
		t.Fatal(err)
	}
	var codex string
	if err := f.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO runtime(workspace_id,owner_id,daemon_id,harness) VALUES($1,$2,'skills-daemon','codex') RETURNING id`, f.ws, f.s.userID(t, f.jwt)).Scan(&codex); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	older := now.Add(-time.Hour)
	ar := machineUsageRun(t, f, f.ws, f.rt, a, "Ada title", &older)
	br := machineUsageRun(t, f, f.ws, codex, b, "Bob title", &now)
	if _, err := f.s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET harness='codex' WHERE id=$1`, br); err != nil {
		t.Fatal(err)
	}
	own := machineUsageRun(t, f, f.ws, f.rt, f.jwt, "Owner title", &now)
	machineUsageRow(t, f, f.ws, ar, 1, now, 10, 5, 2, 3, .25)
	machineUsageRow(t, f, f.ws, ar, 2, now, 20, 7, 4, 6, .5)
	machineUsageRow(t, f, f.ws, br, 1, now, 30, 8, 0, 0, 1)
	machineUsageRow(t, f, f.ws, own, 1, now, 999, 999, 0, 0, 99)
	got := machineUsageRead(t, f, f.ws)
	if len(got.Runs) != 2 || got.Runs[0].RunID != br || got.Runs[1].RunID != ar {
		t.Fatalf("runs: %+v", got.Runs)
	}
	if r := got.Runs[1]; r.Colleague != "Ada" || r.StudyTitle != "Ada title" || r.Harness != "claude" || r.Status != "completed" || r.StartedAt == nil || !r.StartedAt.Equal(older.Truncate(time.Microsecond)) || r.InputTokens != 45 || r.OutputTokens != 12 || r.CostUSD != .75 {
		t.Fatalf("Ada run: %+v", r)
	}
	if r := got.Runs[0]; r.Colleague != "usage-b@lab.org" || r.Harness != "codex" || r.InputTokens != 30 || r.OutputTokens != 8 || r.CostUSD != 1 {
		t.Fatalf("Bob run: %+v", r)
	}
	totals := map[string][2]machineUsageWindow{}
	for _, v := range got.Totals {
		totals[v.Colleague] = [2]machineUsageWindow{v.Last7Days, v.Last30Days}
	}
	want := map[string][2]machineUsageWindow{"Ada": {{57, .75}, {57, .75}}, "usage-b@lab.org": {{38, 1}, {38, 1}}}
	if !reflect.DeepEqual(totals, want) {
		t.Fatalf("totals: %+v", totals)
	}
}

// Catches treating workspace administrators, guests, and outsiders as machine owners.
func TestOnlyTheOwnerSeesThem(t *testing.T) {
	f := runtimeSkillSetup(t)
	for _, role := range []string{"admin", "member"} {
		jwt := f.s.member(t, f.jwt, f.ws, role+"-usage@lab.org", role)
		if code := f.s.call(t, "GET", "/api/machines/skills-daemon/colleagues", jwt, f.ws, nil, nil); code != 404 {
			t.Fatalf("%s: %d", role, code)
		}
	}
	outsider := f.s.signIn(t, "outsider-usage@lab.org")
	for _, tc := range []struct{ jwt, path, ws string }{{outsider, "skills-daemon", f.ws}, {f.jwt, "missing", f.ws}, {f.jwt, "skills-daemon", f.s.workspace(t, f.jwt, "other").ID}} {
		if code := f.s.call(t, "GET", "/api/machines/"+tc.path+"/colleagues", tc.jwt, tc.ws, nil, nil); code != 404 {
			t.Fatalf("unauthorized %+v: %d", tc, code)
		}
	}
	got := machineUsageRead(t, f, f.ws)
	if got.Runs == nil || got.Totals == nil || len(got.Runs) != 0 || len(got.Totals) != 0 {
		t.Fatalf("empty: %+v", got)
	}
}

// Catches serializing full run/card structs or transcript/record joins.
func TestNoStudyContentIsReturned(t *testing.T) {
	f := runtimeSkillSetup(t)
	guest := f.s.member(t, f.jwt, f.ws, "private-usage@lab.org", "member")
	now := time.Now().UTC().Add(-time.Minute)
	run := machineUsageRun(t, f, f.ws, f.rt, guest, "Public card title", &now)
	if _, err := f.s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_message(workspace_id,agent_run_id,attempt,seq,type,content) VALUES($1,$2,1,1,'text','"private transcript sentinel"')`, f.ws, run); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Env.Owner.Exec(t.Context(), `INSERT INTO record(workspace_id,card_id,board_column,round,kind,role,body) SELECT workspace_id,card_id,'grounding',1,'note','leader','{"secret":"private record sentinel"}' FROM agent_run WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if code := f.s.call(t, "GET", "/api/machines/skills-daemon/colleagues", f.jwt, f.ws, nil, &got); code != 200 {
		t.Fatal(code)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"question", "prompt", "transcript", "records", "private record sentinel", "private transcript sentinel", "private question sentinel", "private prompt sentinel"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("private content %q returned: %s", secret, data)
		}
	}
	runs, ok := got["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs: %+v", got)
	}
	r := runs[0].(map[string]any)
	if len(r) != 9 || r["study_title"] != "Public card title" || r["input_tokens"] != float64(0) || r["cost_usd"] != float64(0) {
		t.Fatalf("safe projection: %+v", r)
	}
}

// Catches grouping across workspaces/owners/machines and limiting totals to displayed runs.
func TestMachineUsageIncludesHistoricalRunsAndLimitsOnlyTheList(t *testing.T) {
	f := runtimeSkillSetup(t)
	guest := f.s.member(t, f.jwt, f.ws, "history-usage@lab.org", "member")
	now := time.Now().UTC().Add(-time.Minute)
	old := now.Add(-60 * 24 * time.Hour)
	historical := machineUsageRun(t, f, f.ws, f.rt, guest, "Historical title", &old)
	machineUsageRow(t, f, f.ws, historical, 1, now, 500, 100, 0, 0, 8)
	var cardID string
	if err := f.s.Env.Owner.QueryRow(t.Context(), `SELECT card_id FROM agent_run WHERE id=$1`, historical).Scan(&cardID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run(workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,runtime_id,started_at) SELECT $1,$2,$3,'grounding',1,'leader','claude','private','completed',$4,$5::timestamptz - n*interval '1 second' FROM generate_series(1,201) n`, f.ws, cardID, f.s.userID(t, guest), f.rt, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_machine_attempt(workspace_id,agent_run_id,attempt,runtime_id,daemon_id,owner_id,responsible_user_id,accepted_at,started_at)
      SELECT r.workspace_id,r.id,1,rt.id,rt.daemon_id,rt.owner_id,r.responsible_user_id,r.started_at,r.started_at FROM agent_run r JOIN runtime rt ON rt.id=r.runtime_id AND rt.workspace_id=r.workspace_id WHERE r.workspace_id=$1 ON CONFLICT DO NOTHING`, f.ws); err != nil {
		t.Fatal(err)
	}
	// A separate owner of a different harness with the same daemon id is excluded.
	var otherRT string
	if err := f.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO runtime(workspace_id,owner_id,daemon_id,harness) VALUES($1,$2,'skills-daemon','codex') RETURNING id`, f.ws, f.s.userID(t, guest)).Scan(&otherRT); err != nil {
		t.Fatal(err)
	}
	foreign := machineUsageRun(t, f, f.ws, otherRT, guest, "Other owner title", &now)
	machineUsageRow(t, f, f.ws, foreign, 1, now, 900, 100, 0, 0, 99)
	var separateRT string
	if err := f.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO runtime(workspace_id,owner_id,daemon_id,harness) VALUES($1,$2,'other-machine','claude') RETURNING id`, f.ws, f.s.userID(t, f.jwt)).Scan(&separateRT); err != nil {
		t.Fatal(err)
	}
	separate := machineUsageRun(t, f, f.ws, separateRT, guest, "Other machine title", &now)
	machineUsageRow(t, f, f.ws, separate, 1, now, 100, 100, 0, 0, 44)
	ws2 := f.s.workspace(t, f.jwt, "second-usage-lab").ID
	f.s.member(t, f.jwt, ws2, "history-usage@lab.org", "member")
	var crossRT string
	if err := f.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO runtime(workspace_id,owner_id,daemon_id,harness) VALUES($1,$2,'skills-daemon','claude') RETURNING id`, ws2, f.s.userID(t, f.jwt)).Scan(&crossRT); err != nil {
		t.Fatal(err)
	}
	cross := machineUsageRun(t, f, ws2, crossRT, guest, "Cross workspace title", &now)
	machineUsageRow(t, f, ws2, cross, 1, now, 800, 100, 0, 0, 77)
	// Historical charges remain attributable after a colleague leaves the workspace.
	if _, err := f.s.Env.Owner.Exec(t.Context(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.ws, f.s.userID(t, guest)); err != nil {
		t.Fatal(err)
	}
	got := machineUsageRead(t, f, f.ws)
	if len(got.Runs) != 200 || len(got.Totals) != 1 || got.Totals[0].Last7Days != (machineUsageWindow{600, 8}) || got.Totals[0].Last30Days != (machineUsageWindow{600, 8}) {
		t.Fatalf("historical totals/list: %+v", got)
	}
	for i, r := range got.Runs {
		if r.RunID == historical || r.RunID == foreign || r.RunID == cross || r.RunID == separate {
			t.Fatalf("unexpected run %+v", r)
		}
		if i > 0 && !got.Runs[i-1].StartedAt.After(*r.StartedAt) {
			t.Fatal("list is not newest first")
		}
	}
	other := machineUsageRead(t, f, ws2)
	if len(other.Runs) != 1 || other.Runs[0].RunID != cross || len(other.Totals) != 1 || other.Totals[0].Last7Days != (machineUsageWindow{900, 77}) {
		t.Fatalf("other workspace: %+v", other)
	}
}

// Catches filtering totals by run start time, dropping cache tokens, and including future usage.
func TestMachineUsageRollingWindows(t *testing.T) {
	f := runtimeSkillSetup(t)
	guest := f.s.member(t, f.jwt, f.ws, "windows-usage@lab.org", "member")
	now := time.Now().UTC()
	old := now.Add(-90 * 24 * time.Hour)
	run := machineUsageRun(t, f, f.ws, f.rt, guest, "Window title", &old)
	for i, tc := range []struct {
		at     time.Time
		tokens int64
		cost   float64
	}{
		{now.Add(-7*24*time.Hour + time.Minute), 1, 1},
		{now.Add(-7*24*time.Hour - time.Minute), 10, 2},
		{now.Add(-30*24*time.Hour + time.Minute), 100, 4},
		{now.Add(-30*24*time.Hour - time.Minute), 1000, 8},
		{now.Add(time.Hour), 10000, 16},
	} {
		machineUsageRow(t, f, f.ws, run, i+1, tc.at, tc.tokens, tc.tokens, tc.tokens, tc.tokens, tc.cost)
	}
	got := machineUsageRead(t, f, f.ws)
	if len(got.Totals) != 1 || got.Totals[0].Last7Days != (machineUsageWindow{4, 1}) || got.Totals[0].Last30Days != (machineUsageWindow{444, 7}) {
		t.Fatalf("windows: %+v", got.Totals)
	}
	if len(got.Runs) != 1 || got.Runs[0].InputTokens != 33333 || got.Runs[0].OutputTokens != 11111 || got.Runs[0].CostUSD != 31 {
		t.Fatalf("all-time run usage: %s", fmt.Sprint(got.Runs))
	}
}

func TestMachineUsageEndpointRetainsDeletedRuntimeHistory(t *testing.T) {
	f := runtimeSkillSetup(t)
	guest := f.s.member(t, f.jwt, f.ws, "retained@lab.org", "member")
	now := time.Now().Add(-time.Minute)
	run := machineUsageRun(t, f, f.ws, f.rt, guest, "Retained title", &now)
	machineUsageRow(t, f, f.ws, run, 1, now, 8, 2, 0, 0, 1)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.s.Env.Owner.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO runtime(workspace_id,owner_id,daemon_id,harness) VALUES($1,$2,'skills-daemon','codex')`, f.ws, f.s.userID(t, f.jwt))
	exec(`DELETE FROM runtime WHERE id=$1`, f.rt)
	check := func() {
		t.Helper()
		got := machineUsageRead(t, f, f.ws)
		if len(got.Runs) != 1 || got.Runs[0].RunID != run || got.Runs[0].CostUSD != 1 || len(got.Totals) != 1 || got.Totals[0].Last7Days != (machineUsageWindow{10, 1}) {
			t.Fatalf("retained history: %+v", got)
		}
	}
	check()
	exec(`INSERT INTO runtime(workspace_id,owner_id,daemon_id,harness) VALUES($1,$2,'skills-daemon','claude')`, f.ws, f.s.userID(t, f.jwt))
	check()
	// Surviving historical attribution does not grant endpoint ownership to guests.
	if code := f.s.call(t, "GET", "/api/machines/skills-daemon/colleagues", guest, f.ws, nil, nil); code != 404 {
		t.Fatalf("guest got history: %d", code)
	}
}
