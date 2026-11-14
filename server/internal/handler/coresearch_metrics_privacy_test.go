package handler

import (
	"encoding/json"
	"testing"

	db "lykeion/server/pkg/db/generated"
)

// Shared project/machine metadata remains visible, but a member must never
// receive another person's kernel or machine samples in the list JSON.
func TestCoresearchChatMetricsAreOwnerOnly(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	if code := csAdd(t, s, a, b, ws, p.ID, "member"); code != 201 {
		t.Fatal(code)
	}
	_, first := csChat(t, s, a, ws, p.ID)
	_, second := csChat(t, s, b, ws, p.ID)
	for i, id := range []string{first.ID.String(), second.ID.String()} {
		if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_chat SET kernel_metrics=jsonb_build_array(jsonb_build_object('rss',$2::int)),kernel_state=$3,kernel_seen_at=$4::timestamptz WHERE id=$1`, id, 101+i, []string{"idle", "busy"}[i], []string{"2026-09-01T01:02:03Z", "2026-09-02T04:05:06Z"}[i]); err != nil {
			t.Fatal(err)
		}
	}
	for index, token := range []string{a, b} {
		t.Run([]string{"first member", "second member"}[index], func(t *testing.T) {
			var rows []map[string]json.RawMessage
			if code := s.call(t, "GET", csProjects+"/"+p.ID+"/chats", token, ws, nil, &rows); code != 200 || len(rows) != 2 {
				t.Fatalf("list: %d %+v", code, rows)
			}
			own := []string{first.ID.String(), second.ID.String()}[index]
			for _, raw := range rows {
				var row db.ListCoresearchChatsRow
				bytes, _ := json.Marshal(raw)
				if err := json.Unmarshal(bytes, &row); err != nil {
					t.Fatal(err)
				}
				if row.Cli != "claude" || row.ProjectID.String() != p.ID {
					t.Fatalf("shared metadata missing: %+v", row)
				}
				var samples []struct {
					RSS int `json:"rss"`
				}
				if err := json.Unmarshal(row.KernelMetrics, &samples); err != nil {
					t.Fatal(err)
				}
				if row.ID.String() == own {
					if row.KernelState != []string{"idle", "busy"}[index] || row.KernelSeenAt == nil || row.KernelSeenAt.UTC().Format("2006-01-02T15:04:05Z") != []string{"2026-09-01T01:02:03Z", "2026-09-02T04:05:06Z"}[index] {
						t.Fatalf("own kernel metadata missing: %s", bytes)
					}
					if len(samples) != 1 || samples[0].RSS != 101+index {
						t.Fatalf("own samples missing: %s", row.KernelMetrics)
					}
				} else if raw["kernel_state"] != nil || raw["kernel_seen_at"] != nil || len(samples) != 0 {
					t.Fatalf("other member received kernel metadata: state=%s seen=%s metrics=%s", raw["kernel_state"], raw["kernel_seen_at"], row.KernelMetrics)
				}
			}
		})
	}
}

func TestRuntimeMetricsAreOwnerOnly(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	first := csRegister(t, s, a, ws, "first-machine", "claude")
	second := csRegister(t, s, b, ws, "second-machine", "claude")
	for i, id := range []string{first.RuntimeID.String(), second.RuntimeID.String()} {
		if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET metrics=jsonb_build_object('mem_used',$2::int),models='["model"]' WHERE id=$1`, id, 201+i); err != nil {
			t.Fatal(err)
		}
	}
	for index, token := range []string{a, b} {
		t.Run([]string{"first member", "second member"}[index], func(t *testing.T) {
			var rows []db.ListRuntimesRow
			if code := s.call(t, "GET", "/api/runtimes", token, ws, nil, &rows); code != 200 || len(rows) != 2 {
				t.Fatalf("list: %d %+v", code, rows)
			}
			own := []string{first.RuntimeID.String(), second.RuntimeID.String()}[index]
			for _, row := range rows {
				if row.DeviceName == "" || row.Harness != "claude" || string(row.Models) != `["model"]` {
					t.Fatalf("shared metadata missing: %+v", row)
				}
				var metrics map[string]int
				if len(row.Metrics) != 0 {
					if err := json.Unmarshal(row.Metrics, &metrics); err != nil {
						t.Fatal(err)
					}
				}
				if row.ID.String() == own {
					if metrics["mem_used"] != 201+index {
						t.Fatalf("own metrics missing: %s", row.Metrics)
					}
				} else if len(metrics) != 0 {
					t.Fatalf("other member received machine metrics: %s", row.Metrics)
				}
			}
		})
	}
}
