package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/service"
)

func TestComputeOnlyOwnKernels(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	_, ca := csChat(t, s, a, ws, p.ID)
	_, cb := csChat(t, s, b, ws, p.ID)
	ra := csRegister(t, s, a, ws, "compute-a", "claude")
	rb := csRegister(t, s, b, ws, "compute-b", "claude")
	for i, c := range []string{ca.ID.String(), cb.ID.String()} {
		r := []string{ra.RuntimeID.String(), rb.RuntimeID.String()}[i]
		if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_chat SET daemon_id=$2,kernel_state='idle',kernel_name='python3',kernel_metrics='[{"rss":123,"cpu":1}]' WHERE id=$1`, c, []string{"compute-a", "compute-b"}[i]); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET metrics='{"mem_used":456}',models='[]' WHERE id=$1`, r); e != nil {
			t.Fatal(e)
		}
	}
	for i, token := range []string{a, b} {
		var result json.RawMessage
		code := s.call(t, "GET", csProjects+"/"+p.ID+"/compute", token, ws, nil, &result)
		if code != 200 {
			t.Fatalf("compute %d", code)
		}
		for _, own := range []string{[]string{ca.ID.String(), cb.ID.String()}[i], []string{ra.RuntimeID.String(), rb.RuntimeID.String()}[i], `"rss":123`, `"mem_used":456`} {
			if !strings.Contains(string(result), own) {
				t.Fatalf("missing %s: %s", own, result)
			}
		}
		for _, other := range []string{[]string{cb.ID.String(), ca.ID.String()}[i], []string{rb.RuntimeID.String(), ra.RuntimeID.String()}[i]} {
			if strings.Contains(string(result), other) {
				t.Fatalf("leak %s", result)
			}
		}
	}
}
func TestStopKernelReason(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, c := csChat(t, s, a, ws, p.ID)
	body := map[string]string{"op": "stop", "reason": "Memory is needed elsewhere"}
	if code := s.call(t, "POST", path+"/kernel", b, ws, body, nil); code != 403 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", path+"/kernel", a, ws, body, nil); code != 204 {
		t.Fatal(code)
	}
	var kind, text, author string
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT kind,body,author_user_id::text FROM coresearch_message WHERE chat_id=$1`, c.ID).Scan(&kind, &text, &author); e != nil {
		t.Fatal(e)
	}
	if kind != "comment" || text != "Stopped the kernel: Memory is needed elsewhere" || author != s.userID(t, a) {
		t.Fatalf("%s %s %s", kind, text, author)
	}
}
func TestKernelMetricsRetention(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, c := csChat(t, s, a, ws, p.ID)
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, nil)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var work coresearch.Work
	work = csWork(t, s, a, "work", ref)
	if len(work.Cells) != 1 {
		t.Fatal(work)
	}
	started := time.Now().UTC()
	for i := 0; i < 65; i++ {
		code := s.call(t, "POST", "/api/daemon/coresearch/chats/"+c.ID.String()+"/kernel?daemon_id=work", s.personalToken(t, a), ws, map[string]any{"state": "busy", "kernel": "python3", "sample": map[string]any{"rss": i + 1, "cpu": 1.5}}, nil)
		if code != 204 {
			t.Fatal(code)
		}
	}
	var raw []byte
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT kernel_metrics FROM coresearch_chat WHERE id=$1`, c.ID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var samples []map[string]any
	json.Unmarshal(raw, &samples)
	if len(samples) != 60 || samples[0]["rss"] != float64(6) || samples[59]["rss"] != float64(65) {
		t.Fatalf("%s", raw)
	}
	var result struct {
		Kernels []struct {
			Samples []struct {
				T time.Time `json:"t"`
			} `json:"samples"`
		} `json:"kernels"`
	}
	if code := s.call(t, "GET", csProjects+"/"+p.ID+"/compute", a, ws, nil, &result); code != 200 || len(result.Kernels) != 1 || len(result.Kernels[0].Samples) != 60 {
		t.Fatalf("compute samples %d %+v", code, result)
	}
	for _, sample := range result.Kernels[0].Samples {
		if sample.T.IsZero() || sample.T.Before(started) || sample.T.After(time.Now()) {
			t.Fatalf("missing server timestamp: %+v", sample)
		}
	}
	if _, old := samples[0]["at"]; old {
		t.Fatal("obsolete at timestamp")
	}
}

func TestHeartbeatCompute(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	ra := csRegister(t, s, a, ws, "telemetry", "claude")
	started := time.Now().UTC()
	rb := csRegister(t, s, b, ws, "foreign", "claude")
	body := map[string]any{"runtimes": []service.RuntimeRef{ra}, "metrics": map[string]any{"mem_used": 1, "mem_total": 2, "cpu_used": 10, "cores": 2, "disk_free": 3, "disk_total": 4}, "models": map[string]any{"claude": []map[string]string{{"value": "from-cli", "label": "CLI model", "description": "reported"}}}}
	if code := s.call(t, "POST", "/api/daemon/heartbeat", s.personalToken(t, a), "", body, nil); code != 200 {
		t.Fatal(code)
	}
	var rows []map[string]json.RawMessage
	if code := s.call(t, "GET", "/api/runtimes", a, ws, nil, &rows); code != 200 {
		t.Fatal(code)
	}
	found := false
	for _, row := range rows {
		var id string
		json.Unmarshal(row["id"], &id)
		if id == ra.RuntimeID.String() {
			found = true
			var metrics map[string]any
			if e := json.Unmarshal(row["metrics"], &metrics); e != nil || metrics["mem_used"] != float64(1) || metrics["disk_total"] != float64(4) {
				t.Fatalf("metrics JSON %s %v", row["metrics"], e)
			}
			var stamp struct {
				T time.Time `json:"t"`
			}
			json.Unmarshal(row["metrics"], &stamp)
			if stamp.T.IsZero() || stamp.T.Before(started) || stamp.T.After(time.Now()) {
				t.Fatalf("missing server machine timestamp: %s", row["metrics"])
			}
			if !strings.Contains(string(row["models"]), "from-cli") {
				t.Fatal(string(row["models"]))
			}
		}
	}
	if !found {
		t.Fatal("no runtime")
	}
	body["runtimes"] = []service.RuntimeRef{rb}
	if code := s.call(t, "POST", "/api/daemon/heartbeat", s.personalToken(t, a), "", body, nil); code != 404 {
		t.Fatalf("foreign heartbeat %d", code)
	}
}

func TestComputeRemovedMemberAndRuntimeJSONPrivacy(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	ra := csRegister(t, s, a, ws, "private-compute", "claude")
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET metrics='{"mem_used":9271}' WHERE id=$1`, ra.RuntimeID); e != nil {
		t.Fatal(e)
	}
	var raw json.RawMessage
	if code := s.call(t, "GET", "/api/runtimes", b, ws, nil, &raw); code != 200 || strings.Contains(string(raw), "9271") {
		t.Fatalf("runtime leak: %d %s", code, raw)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `DELETE FROM coresearch_member WHERE project_id=$1 AND user_id=$2`, p.ID, s.userID(t, b)); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "GET", csProjects+"/"+p.ID+"/compute", b, ws, nil, nil); code != 404 {
		t.Fatalf("removed member compute %d", code)
	}
}

func TestHeartbeatInvalidTelemetryDoesNotBlockLiveness(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	ref := csRegister(t, s, a, ws, "bad-metrics", "claude")
	for _, metrics := range []any{nil, "unknown", map[string]any{"mem_used": 10, "mem_total": 1, "cores": 1, "cpu_used": 200}} {
		if code := s.call(t, "POST", "/api/daemon/heartbeat", s.personalToken(t, a), "", map[string]any{"runtimes": []service.RuntimeRef{ref}, "metrics": metrics, "models": map[string]any{"claude": []map[string]string{{"value": "bad", "label": ""}}}}, nil); code != 200 {
			t.Fatal(code)
		}
	}
	for _, models := range []any{[]any{}, "unknown", 17, nil, map[string]any{"claude": "invalid"}} {
		var before time.Time
		if e := s.Env.Owner.QueryRow(t.Context(), `UPDATE runtime SET status='offline',last_seen_at=now()-interval '1 day' WHERE id=$1 RETURNING last_seen_at`, ref.RuntimeID).Scan(&before); e != nil {
			t.Fatal(e)
		}
		if code := s.call(t, "POST", "/api/daemon/heartbeat", s.personalToken(t, a), "", map[string]any{"runtimes": []service.RuntimeRef{ref}, "models": models}, nil); code != 200 {
			t.Fatalf("models=%v rejected liveness: %d", models, code)
		}
		var seen time.Time
		var status string
		if e := s.Env.Owner.QueryRow(t.Context(), `SELECT status,last_seen_at FROM runtime WHERE id=$1`, ref.RuntimeID).Scan(&status, &seen); e != nil || status != "online" || !seen.After(before) {
			t.Fatalf("liveness not advanced: %s %v %v", status, seen, e)
		}
		if code := s.call(t, "POST", "/api/daemon/heartbeat", s.personalToken(t, a), "", map[string]any{"runtimes": []service.RuntimeRef{{}}, "models": models}, nil); code != 400 {
			t.Fatalf("invalid runtime accepted: %d", code)
		}
	}
	var status string
	var metrics []byte
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT status,metrics FROM runtime WHERE id=$1`, ref.RuntimeID).Scan(&status, &metrics); e != nil || status != "online" || len(metrics) != 0 {
		t.Fatalf("%s %s %v", status, metrics, e)
	}
}

func TestComputeUsesReportedKernelName(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, c := csChat(t, s, a, ws, p.ID)
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, nil)
	ref := csRegister(t, s, a, ws, "named-kernel", "claude")
	csWork(t, s, a, "named-kernel", ref)
	endpoint := "/api/daemon/coresearch/chats/" + c.ID.String() + "/kernel?daemon_id=named-kernel"
	if code := s.call(t, "POST", endpoint, s.personalToken(t, a), ws, map[string]any{"state": "idle", "kernel": "python3"}, nil); code != 204 {
		t.Fatalf("publish actual name %d", code)
	}
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "queued-other-name", "source": "1"}, nil)
	var out struct {
		Kernels []struct {
			Kernel string `json:"kernel"`
		}
	}
	if code := s.call(t, "GET", csProjects+"/"+p.ID+"/compute", a, ws, nil, &out); code != 200 || len(out.Kernels) != 1 || out.Kernels[0].Kernel != "python3" {
		t.Fatalf("queued request renamed active kernel: %d %+v", code, out)
	}
	var shared json.RawMessage
	if code := s.call(t, "GET", csProjects+"/"+p.ID+"/chats", b, ws, nil, &shared); code != 200 || strings.Contains(string(shared), `"kernel_name":"python3"`) {
		t.Fatalf("name leaked: %d %s", code, shared)
	}
	if code := s.call(t, "POST", endpoint, s.personalToken(t, a), ws, map[string]string{"state": "stopped"}, nil); code != 204 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", endpoint, s.personalToken(t, a), ws, map[string]any{"kernel": "python3", "sample": map[string]any{"rss": 123, "cpu": 1}}, nil); code != 204 {
		t.Fatal(code)
	}
	var name string
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT kernel_name FROM coresearch_chat WHERE id=$1`, c.ID).Scan(&name); e != nil || name != "" {
		t.Fatalf("stop retained name %q %v", name, e)
	}
	if code := s.call(t, "GET", csProjects+"/"+p.ID+"/compute", a, ws, nil, &out); code != 200 || len(out.Kernels) != 0 {
		t.Fatalf("stopped kernel still active: %d %+v", code, out)
	}
}
