package handler

import (
	"github.com/google/uuid"
	"lykeion/server/internal/storageinfo"
	db "lykeion/server/pkg/db/generated"
	"testing"
	"time"
)

func TestStorageOwnerDedupAndTelemetry(t *testing.T) {
	f := runtimeSkillSetup(t)
	var before storageinfo.Response
	if c := f.s.call(t, "GET", "/api/storage", f.jwt, f.ws, nil, &before); c != 200 || len(before.Machines) != 1 || before.Machines[0].Snapshot != nil {
		t.Fatalf("missing snapshot: %d %+v", c, before)
	}
	var rs []db.Runtime
	if c := f.s.call(t, "POST", "/api/daemon/register", f.token, "", map[string]any{"workspace_id": f.ws, "daemon_id": "skills-daemon", "device_name": "laptop", "harnesses": []string{"claude", "codex"}}, &rs); c != 200 {
		t.Fatal(c)
	}
	snap := storageinfo.Snapshot{MeasuredAt: time.Now().UTC(), Categories: []storageinfo.Category{{Key: "workspaces", Path: "/work", Bytes: 4, Complete: true, FilesystemID: "1"}, {Key: "environments", Path: "/env", Complete: true, FilesystemID: "1"}, {Key: "state", Path: "/state", Complete: false, Error: "unavailable"}}, Filesystems: []storageinfo.Filesystem{{ID: "1", AvailableBytes: 100, Paths: []string{"/work", "/env"}}}}
	in := storageinfo.Report{WorkspaceID: uuid.MustParse(f.ws), RuntimeID: uuid.MustParse(f.rt), DaemonID: "skills-daemon", Snapshot: snap}
	if c := f.s.call(t, "POST", "/daemon/storage", f.token, "", in, nil); c != 204 {
		t.Fatalf("report: %d", c)
	}
	var got storageinfo.Response
	if c := f.s.call(t, "GET", "/api/storage", f.jwt, f.ws, nil, &got); c != 200 || len(got.Machines) != 1 || got.Machines[0].Snapshot == nil || got.Machines[0].Snapshot.Categories[0].Bytes != 4 {
		t.Fatalf("read: %d %+v", c, got)
	}
	in.DaemonID = "wrong"
	if c := f.s.call(t, "POST", "/daemon/storage", f.token, "", in, nil); c != 404 {
		t.Fatalf("identity mismatch %d", c)
	}
	in.DaemonID = "skills-daemon"
	in.Snapshot.Categories[0].Bytes = -1
	if c := f.s.call(t, "POST", "/daemon/storage", f.token, "", in, nil); c != 400 {
		t.Fatalf("negative bytes %d", c)
	}
	if c := f.s.call(t, "POST", "/daemon/storage", f.jwt, "", in, nil); c != 401 {
		t.Fatalf("JWT telemetry %d", c)
	}
	admin := f.s.member(t, f.jwt, f.ws, "storage-admin@lab.org", "admin")
	var hidden storageinfo.Response
	if c := f.s.call(t, "GET", "/api/storage", admin, f.ws, nil, &hidden); c != 200 || len(hidden.Machines) != 0 {
		t.Fatalf("member saw owner paths %d %+v", c, hidden)
	}
	adminToken := f.s.personalToken(t, admin)
	in.Snapshot = snap
	in.Snapshot.Categories[0].Bytes = 4
	if c := f.s.call(t, "POST", "/daemon/storage", adminToken, "", in, nil); c != 404 {
		t.Fatalf("member reported owner's root %d", c)
	}
	if _, err := f.s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET status='offline' WHERE workspace_id=$1`, f.ws); err != nil {
		t.Fatal(err)
	}
	if c := f.s.call(t, "GET", "/api/storage", f.jwt, f.ws, nil, &got); c != 200 || got.Machines[0].Online || got.Machines[0].Snapshot == nil {
		t.Fatalf("offline lost cache %d %+v", c, got)
	}
	in.WorkspaceID = uuid.MustParse(f.s.workspace(t, f.jwt, "other-owner-workspace").ID)
	if c := f.s.call(t, "POST", "/daemon/storage", f.token, "", in, nil); c != 404 {
		t.Fatalf("cross workspace report %d", c)
	}
	other := f.s.signIn(t, "storage-other@lab.org")
	otherWS := f.s.workspace(t, other, "other")
	if c := f.s.call(t, "GET", "/api/storage", other, f.ws, nil, nil); c == 200 {
		t.Fatal("cross-workspace storage exposed")
	}
	var empty storageinfo.Response
	if c := f.s.call(t, "GET", "/api/storage", other, otherWS.ID, nil, &empty); c != 200 || len(empty.Machines) != 0 {
		t.Fatalf("owner leakage %d %+v", c, empty)
	}
}
