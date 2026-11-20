package handler

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
)

const laboratoryReadOnly = "laboratory agents can't be changed"

func TestCreateAgentNeedsStage(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "stage-create@lab.org")
	w := s.workspace(t, a, "lab")
	for name, body := range map[string]map[string]any{
		"empty":   {"name": "A", "harness": "claude", "stage": ""},
		"unknown": {"name": "B", "harness": "claude", "stage": "reporting"},
		"missing": {"name": "C", "harness": "claude"},
	} {
		var out map[string]any
		if code := s.call(t, "POST", "/api/agents", a, w.ID, body, &out); code != 400 || out["error"] != "choose a stage for this agent" {
			t.Fatalf("%s: %d %v", name, code, out)
		}
	}
	var ok struct{ Stage string }
	if code := s.call(t, "POST", "/api/agents", a, w.ID, map[string]any{"name": "D", "harness": "claude", "stage": "hypotheses"}, &ok); code != 201 || ok.Stage != "hypotheses" {
		t.Fatalf("valid: %d %+v", code, ok)
	}
}

func TestPatchAgentSetsStage(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "stage-patch@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.newAgent(t, a, w.ID, "Legacy", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent SET stage = '' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	var out struct{ Stage string }
	if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]any{"stage": "validation"}, &out); code != 200 || out.Stage != "validation" {
		t.Fatalf("patch: %d %+v", code, out)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]any{"stage": "reporting"}, nil); code != 400 {
		t.Fatalf("bad stage: %d", code)
	}
}

func TestAristotleHasNoStage(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "stage-aristotle@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.newAgent(t, a, w.ID, "Aristotle", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent SET stage = '', visibility = 'private', built_in_key = 'aristotle' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]any{"stage": "grounding"}, &out); code != 400 || out["error"] != "Aristotle has no stage" {
		t.Fatalf("patch: %d %v", code, out)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]any{"description": "Lab memory"}, nil); code != 200 {
		t.Fatalf("other fields: %d", code)
	}
}

// laboratoryAgent installs the laboratory and returns one shared agent's id.
func laboratoryAgent(t *testing.T, s *server, auth, ws string) string {
	t.Helper()
	ctx := t.Context()
	if err := s.H.Store.InWorkspace(ctx, uuid.MustParse(ws), uuid.MustParse(s.userID(t, auth)), func(tx *store.Tx) error {
		return pack.InstallShipped(ctx, tx, s.H.Blobs, pack.Shipped())
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.H.Store.InWorkspace(ctx, uuid.MustParse(ws), uuid.Nil, func(tx *store.Tx) error { return team.InstallLaboratory(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT id::text FROM agent WHERE workspace_id = $1 AND laboratory = 'built-in' ORDER BY created_at, id LIMIT 1`, ws).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLaboratoryAgentIsReadOnly(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "stage-lab@lab.org")
	w := s.workspace(t, a, "lab")
	id := laboratoryAgent(t, s, a, w.ID)
	snapshot := func() string {
		var row string
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT row_to_json(agent)::text FROM agent WHERE id = $1`, id).Scan(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	before := snapshot()
	for name, c := range map[string]struct {
		method, path string
		body         any
	}{
		"name":         {"PATCH", "", map[string]any{"name": "Renamed"}},
		"instructions": {"PATCH", "", map[string]any{"instructions": "x"}},
		"model":        {"PATCH", "", map[string]any{"model": "m"}},
		"avatar":       {"PATCH", "", map[string]any{"avatar_url": "emoji:🧪"}},
		"archive":      {"POST", "/archive", nil},
		"delete":       {"DELETE", "", nil},
		"capabilities": {"PUT", "/customize", map[string]any{"skills": []string{}, "connectors": []any{}}},
	} {
		var out map[string]any
		if code := s.call(t, c.method, "/api/agents/"+id+c.path, a, w.ID, c.body, &out); code != 403 || out["error"] != laboratoryReadOnly {
			t.Fatalf("%s: %d %v", name, code, out)
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("row changed:\n%s\n%s", before, after)
	}
}

func TestCannotDuplicateLaboratoryAgent(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "stage-dup@lab.org")
	w := s.workspace(t, a, "lab")
	id := laboratoryAgent(t, s, a, w.ID)
	if code := s.call(t, "POST", "/api/agents/"+id+"/duplicate", a, w.ID, map[string]string{"name": "Mine"}, nil); code != 403 {
		t.Fatalf("duplicate laboratory agent: %d", code)
	}
}

func TestAgentReportTemplate(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "report-template@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.newAgent(t, a, w.ID, "Reporter", "claude")
	var initial struct {
		Default string `json:"default_report_template"`
	}
	if code := s.call(t, "GET", "/api/agents/"+id, a, w.ID, nil, &initial); code != 200 ||
		!strings.Contains(initial.Default, "Write the hand-in answer in free form") ||
		!strings.Contains(initial.Default, "never invent evidence") || strings.Contains(initial.Default, "##") {
		t.Fatalf("default template: %d %+v", code, initial)
	}
	var out struct {
		ReportTemplate string `json:"report_template"`
	}
	for _, template := range []string{"## Results", ""} {
		if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]any{"report_template": template}, &out); code != 200 || out.ReportTemplate != template {
			t.Fatalf("template %q: %d %+v", template, code, out)
		}
	}
	if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]any{"report_template": "bad\x00text"}, nil); code != 400 {
		t.Fatalf("invalid template: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent SET visibility = 'private', built_in_key = 'aristotle' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]any{"report_template": "## Results"}, nil); code != 400 {
		t.Fatalf("built-in template: %d", code)
	}
}
