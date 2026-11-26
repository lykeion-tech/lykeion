package scripts_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lykeion/server/internal/testutil"
)

func TestAuditStructuredBinding(t *testing.T) {
	ctx := context.Background()
	env := testutil.Setup(t)
	type object = map[string]any
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.New
	owner, ws, other := id(), id(), id()
	exec(`INSERT INTO "user" (id, email) VALUES ($1, 'audit@example.test')`, owner)
	exec(`INSERT INTO workspace (id, slug, name) VALUES ($1, 'audit', 'private'), ($2, 'other', 'private')`, ws, other)
	day := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	card := func(workspace uuid.UUID, number, round int, stage string, created time.Time) uuid.UUID {
		t.Helper()
		study := id()
		exec(`INSERT INTO card (workspace_id, id, number, owner_id, title, question, round, board_column, created_at)
			VALUES ($1, $2, $3, $4, 'private title', 'private study question', $5, $6, $7)`,
			workspace, study, number, owner, round, stage, created)
		return study
	}
	mixed := card(ws, 1, 10, "experimentation", day)
	old := card(ws, 2, 4, "experimentation", day)
	empty := card(ws, 3, 0, "grounding", day.AddDate(0, 0, 7))
	outsider := card(other, 1, 1, "experimentation", day)

	type fixture struct{ workspace, study uuid.UUID }
	now := day
	record := func(f fixture, stage string, round int, kind, role, key string, body object, run *uuid.UUID) uuid.UUID {
		t.Helper()
		rec := id()
		exec(`INSERT INTO record (id, workspace_id, card_id, board_column, round, kind, role, key, body, agent_run_id, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, rec, f.workspace, f.study, stage, round, kind, role, key, body, run, now)
		return rec
	}
	run := func(f fixture, stage string, round int, role, status string) uuid.UUID {
		t.Helper()
		key := id()
		kind, harness, attempt := "role", "claude", 1
		if role == "rerun" {
			kind, harness = "rerun", ""
		}
		if status == "queued" {
			attempt = 0
		}
		exec(`INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round,
			role, kind, status, harness, prompt, attempt, created_at, completed_at, requires_output_attestation)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'private prompt',$11,$12,
			CASE WHEN $9 = 'completed' THEN $12::timestamptz END, $9 = 'queued' AND $8 = 'rerun')`,
			key, f.workspace, f.study, owner, stage, round, role, kind, status, harness, attempt, now)
		return key
	}
	advance := func() { now = now.Add(time.Minute) }
	handIn := func(f fixture, stage string, round int, role, status, kind string, modern bool, data map[string]object, replies ...object) uuid.UUID {
		t.Helper()
		advance()
		ar := run(f, stage, round, role, status)
		var converted []uuid.UUID
		for key, body := range data {
			converted = append(converted, record(f, stage, round, kind, role, key, body, &ar))
		}
		body := object{"attempt": 1, "hand_in": object{"status": "done", "replies": replies}}
		if modern {
			body["converted_records"] = converted
		}
		record(f, stage, round, "hand_in", role, "", body, &ar)
		return ar
	}
	server := func(f fixture, stage string, round int, kind string, body object) {
		t.Helper()
		advance()
		record(f, stage, round, kind, "server", "", body, nil)
	}
	value := func(name, number string, binding ...string) object {
		b := object{"name": name, "value": number, "file": "private.json"}
		if len(binding) > 0 {
			b["field"] = binding[0]
		}
		if len(binding) > 1 {
			b["measure"] = binding[1]
		}
		return b
	}
	check := func(name, status string) object { return object{"id": name, "by": "server", "status": status} }
	item := func(key, label, status string, checks ...object) object {
		return object{"kind": "value", "key": key, "label": label, "identity": label, "status": status, "checks": checks}
	}
	e, h, v := "experimentation", "hypotheses", "validation"
	f := fixture{ws, mixed}
	hkey := "h1"
	decision := id().String()
	server(f, h, 2, "hypothesis", object{"text": "unaccepted", "comparison": object{"threshold": 0.5}})
	record(f, h, 2, "hypothesis", "leader", hkey, object{
		"text": "private comparison", "comparison": object{"threshold": 0.5},
	}, nil)
	server(f, h, 2, "server_checks", object{"complete": true, "approval_decision": decision, "checks": object{
		"items": []object{{"kind": "hypothesis", "key": hkey, "status": "pass"}},
	}})
	server(f, h, 2, "outcome", object{"decision_id": decision, "verdict": "approved_by_owner"})
	original := handIn(f, e, 3, "leader", "completed", "value", true, map[string]object{
		"b1": value("slope", "0.58", "/wrong", "slope"),
		"b2": value("wrong unit", "56", "/rate", "rate"),
		"b3": value("unknown id", "0.2", "/rate"),
		"b4": value("clean", "1e-5", "", "rate"),
		"b5": value("textual", "above baseline"),
		"b6": value("pending failure", "20", "/rate", "rate"),
	})
	record(f, e, 3, "run", "leader", "x1", object{"tool": "run_script", "output_hashes": object{"private.json": "sha"}}, &original)
	handIn(f, e, 3, "member", "completed", "value", true, map[string]object{"other": value("member", "3")})
	handIn(f, e, 3, "leader", "failed", "value", true, map[string]object{"failed": value("discarded", "3")})
	failed := []object{
		item("b1", "0.58: slope", "fail", check("bound", "fail"), check("measure", "pass")),
		item("b2", "56: wrong unit", "fail", check("bound", "fail"), check("measure", "pass"), check("scale", "fail")),
		item("b3", "0.2: unknown id", "fail", check("bound", "pass"), check("measure", "fail")),
	}
	server(f, e, 3, "server_checks", object{"items": failed})
	server(f, e, 3, "server_checks", object{"items": []object{item("b6", "temporary failure", "fail", check("scale", "fail"))}})
	server(f, e, 3, "review", object{"decision": "redo", "items": failed})
	server(f, e, 3, "outcome", object{"verdict": "redo"})
	handIn(f, e, 4, "leader", "completed", "value", true, map[string]object{
		"b7": value("slope", "0.5833", "/right", "slope"),
		"b8": value("wrong unit", "0.56", "/rate", "rate"), // no reply, so a distinct item
	}, object{"action": "corrected", "item": "  0.58:   SLOPE  "}, object{"action": "withdrawn", "item": "wrong unit"})
	server(f, e, 4, "server_checks", object{"complete": true, "checks": object{"items": []object{
		item("b7", "slope", "pass", check("bound", "pass"), check("measure", "pass")),
		item("b8", "wrong unit", "pass", check("bound", "pass"), check("measure", "pass")),
	}}})
	server(f, e, 4, "review", object{"decision": "block", "items": []object{
		item("b2", "wrong unit", "withdrawn", check("withdrawn", "withdrawn")),
		failed[2], // a carried failure should not count a second time
	}})

	assessment := object{"hypothesis": "private comparison", "measurement": "private measurement"}
	for _, sample := range []struct {
		round  int
		code   string
		scoped bool
	}{
		{5, "invalid", false}, {7, "refuted", true}, {8, "inconclusive", false}, {9, "", false},
	} {
		handIn(f, v, sample.round, "leader", "completed", "assessment", true, map[string]object{
			"a" + sample.code:          assessment,
			"unaccepted" + sample.code: {"hypothesis": "unaccepted", "measurement": "private measurement"},
		})
		if sample.code == "" {
			run(f, v, sample.round, "rerun", "queued")
			continue
		}
		ar := run(f, v, sample.round, "rerun", "completed")
		record(f, v, sample.round, "rerun", "server", "", object{
			"scoped_outputs": sample.scoped,
			"unavailable":    object{"private measurement": sample.code == "inconclusive"},
		}, &ar)
		server(f, v, sample.round, "validation_status", object{
			"hypothesis": "private comparison", "status": sample.code, "final": false,
		})
		server(f, v, sample.round, "validation_status", object{
			"hypothesis": "private comparison", "status": "inconclusive", "final": true,
		})
	}
	handIn(fixture{ws, old}, e, 4, "agent", "completed", "value", false, map[string]object{
		"legacy": value("legacy number", "-3.5"),
	})
	handIn(fixture{other, outsider}, e, 1, "agent", "completed", "value", true, map[string]object{
		"b1": value("slope", "32", "/elsewhere", "slope"),
	})

	sql, err := os.ReadFile("audit-structured-binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(sql), "BEGIN READ ONLY;")
	end := strings.LastIndex(string(sql), "ROLLBACK;")
	if start < 0 || end <= start {
		t.Fatal("expected a read-only transaction with a rollback")
	}
	begin := string(sql[:start+len("BEGIN READ ONLY;")])
	query := string(sql[start+len("BEGIN READ ONLY;") : end])
	want := func(kind, week string, study *uuid.UUID, current, reached string, round int32, counts object) object {
		o := object{"workspace_id": ws, "row_kind": kind, "week": week, "study_id": nil,
			"current_stage": nil, "stage_reached": nil, "round": nil, "daemon_version": nil,
			"studies": int64(1)}
		if study != nil {
			o["study_id"], o["current_stage"], o["stage_reached"], o["round"] = *study, current, reached, round
		}
		for _, name := range strings.Fields(`
			experimentation_rounds validation_rounds attested_experimentation_runs queued_attestation_replays
			scoped_replays legacy_or_unmarked_replays studies_without_settled_experimentation
			bound unbound binding_blocked blocked_by_bound blocked_by_measure blocked_by_scale
			binding_blocked_then_corrected binding_blocked_then_withdrawn binding_blocked_unresolved
			comparison_valid comparison_invalid comparison_invalid_then_corrected comparison_inconclusive
			comparison_inconclusive_older_daemon comparison_pending`) {
			o[name] = int64(0)
		}
		for k, n := range counts {
			o[k] = int64(n.(int))
		}
		return o
	}
	mixedCounts := object{
		"experimentation_rounds": 2, "validation_rounds": 4, "attested_experimentation_runs": 1,
		"queued_attestation_replays": 1, "scoped_replays": 1, "legacy_or_unmarked_replays": 2,
		"bound": 5, "unbound": 1, "binding_blocked": 3,
		"blocked_by_bound": 2, "blocked_by_measure": 1, "blocked_by_scale": 1,
		"binding_blocked_then_corrected": 1, "binding_blocked_then_withdrawn": 1, "binding_blocked_unresolved": 1,
		"comparison_valid": 1, "comparison_invalid": 1, "comparison_invalid_then_corrected": 1,
		"comparison_inconclusive": 1, "comparison_inconclusive_older_daemon": 1, "comparison_pending": 1,
	}
	monday, nextMonday := "2026-11-16", "2026-11-23"
	noExperiment := object{"studies_without_settled_experimentation": 1}
	expected := map[string]object{
		mixed.String(): want("study", monday, &mixed, e, v, 10, mixedCounts),
		old.String():   want("study", monday, &old, e, e, 4, object{"unbound": 1, "experimentation_rounds": 1}),
		empty.String(): want("study", nextMonday, &empty, "grounding", "grounding", 0, noExperiment),
		nextMonday:     want("workspace_week", nextMonday, nil, "", "", 0, noExperiment),
	}
	mixedCounts["unbound"], mixedCounts["experimentation_rounds"], mixedCounts["studies"] = 2, 3, 2
	expected[monday] = want("workspace_week", monday, nil, "", "", 0, mixedCounts)
	mixedCounts["studies"], mixedCounts["studies_without_settled_experimentation"] = 3, 1
	expected["workspace"] = want("workspace", "", nil, "", "", 0, mixedCounts)
	expected["workspace"]["week"] = nil

	for _, tc := range []struct {
		name     string
		pool     *pgxpool.Pool
		scope    string
		expected map[string]object
	}{
		{"scoped app", env.App, ws.String(), expected},
		{"scoped owner", env.Owner, ws.String(), expected},
		{"unscoped owner", env.Owner, "", map[string]object{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := tc.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			if _, err := conn.Exec(ctx, begin); err != nil {
				t.Fatal(err)
			}
			defer conn.Exec(ctx, string(sql[end:]))
			if _, err := conn.Exec(ctx, `SELECT set_config('app.workspace_id', $1, true)`, tc.scope); err != nil {
				t.Fatal(err)
			}
			var mode string
			if err := conn.QueryRow(ctx, "SHOW transaction_read_only").Scan(&mode); err != nil || mode != "on" {
				t.Fatalf("transaction_read_only = %q, error %v", mode, err)
			}
			rows, err := conn.Query(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			got := map[string]object{}
			for rows.Next() {
				values, err := rows.Values()
				if err != nil {
					t.Fatal(err)
				}
				row := object{}
				for i, col := range rows.FieldDescriptions() {
					value := values[i]
					switch x := value.(type) {
					case [16]byte:
						value = uuid.UUID(x)
					case time.Time:
						value = x.Format("2006-01-02")
					}
					row[col.Name] = value
				}
				key := "workspace"
				if week, ok := row["week"].(string); ok {
					key = week
				}
				if study, ok := row["study_id"].(uuid.UUID); ok {
					key = study.String()
				}
				if _, exists := got[key]; exists {
					t.Fatalf("duplicate row %s", key)
				}
				got[key] = row
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.expected) {
				actual, _ := json.MarshalIndent(got, "", "  ")
				expected, _ := json.MarshalIndent(tc.expected, "", "  ")
				t.Fatalf("got %s\nwant %s", actual, expected)
			}
		})
	}
}
