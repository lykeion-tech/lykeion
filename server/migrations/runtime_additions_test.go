package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	db "lykeion/server/pkg/db/generated"
)

func TestRuntimeAdditionsMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	refuse := func(sql, code string, args ...any) {
		t.Helper()
		_, err := env.Owner.Exec(ctx, sql, args...)
		pgerr, ok := err.(*pgconn.PgError)
		if !ok || pgerr.Code != code {
			t.Fatalf("want SQLSTATE %s, got %v", code, err)
		}
	}
	read := func(name string) string {
		t.Helper()
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// The columns this checks are dropped by 0030; its down puts them back.
	exec(read("0076_remove_ratings_and_preserve_run_history.down.sql"))
	exec(read("0030_one_agent_studies.down.sql"))
	ins := `INSERT INTO attachment (workspace_id, uploader_id, card_id, name, content_type, size_bytes, sha256, blob_key)
		VALUES ($1, $2, $3, 'a.zip', 'application/zip', $4, 'h', 'k')`
	exec(ins, ws, u, card, int64(5368709120))
	refuse(ins, "23514", ws, u, card, int64(5368709121))
	refuse(ins, "23514", ws, u, card, int64(-1))
	for _, values := range []struct {
		inner int
		bytes int64
	}{
		{-1, 0}, {20001, 0}, {0, -1},
	} {
		refuse(`UPDATE attachment SET inner_files=$1, unpacked_bytes=$2`, "23514", values.inner, values.bytes)
	}
	exec(`UPDATE attachment SET inner_files=20000, unpacked_bytes=5368709120`)
	service := `INSERT INTO attachment (workspace_id, uploader_id, card_id, name, content_type, size_bytes, sha256, blob_key, kind, service_name, service_start)
		VALUES ($1, $2, $3, 'service.zip', 'application/zip', 1, 'h', 'k', 'service', $4, $5)`
	exec(service, ws, u, nil, nil, nil)
	refuse(service, "23514", ws, u, card, nil, "start")
	refuse(service, "23514", ws, u, card, "db", nil)
	refuse(`UPDATE attachment SET card_id=$1 WHERE kind='service' AND card_id IS NULL`, "23514", card)
	for _, metadata := range []string{
		`service_name='db'`, `service_start='start'`, `service_client='client.py'`, `service_env=ARRAY['DATA']`,
	} {
		refuse(`UPDATE attachment SET `+metadata+` WHERE kind='file'`, "23514")
	}
	refuse(`UPDATE attachment SET kind='unknown'`, "23514")
	exec(service, ws, u, card, "db", "start")
	refuse(service, "23505", ws, u, card, "db", "start other")
	// Pending uploads may reuse a service name until a study claims them.
	exec(service, ws, u, nil, "db", "start")

	var defaultEnv string
	if err := env.Owner.QueryRow(ctx, `SELECT env::text FROM card WHERE id=$1`, card).Scan(&defaultEnv); err != nil || defaultEnv != "{}" {
		t.Fatalf("default env = %q (%v), want {}", defaultEnv, err)
	}
	for _, bad := range []any{nil, `[]`, `null`, `"value"`, `{"COUNT":1}`, `{"NESTED":{}}`, `{"LIST":[]}`, `{"BOOL":true}`, `{"NULL":null}`} {
		code := "23514"
		if bad == nil {
			code = "23502"
		}
		refuse(`UPDATE card SET env=$1 WHERE id=$2`, code, bad, card)
	}
	exec(`UPDATE card SET env='{"DATA":"/data","COUNT":"1"}' WHERE id=$1`, card)

	// Rollback must refuse incompatible rows without deleting them.
	refuse(read("0022_runtime_additions.down.sql"), "23514")
	var count int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM attachment WHERE size_bytes > 104857600`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback retained %d large rows (%v), want 1", count, err)
	}
	exec(`DELETE FROM attachment WHERE size_bytes > 104857600`)
	for range 2 {
		exec(read("0022_runtime_additions.down.sql"))
		refuse(ins, "23514", ws, u, card, int64(104857601))
		exec(read("0022_runtime_additions.up.sql"))
		exec(ins, ws, u, card, int64(5368709120))
		exec(`DELETE FROM attachment WHERE size_bytes > 104857600`)
	}
}

func TestRuntimeAdditionsQueries(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	q := db.New(env.Owner)
	insert := func(kind string) db.Attachment {
		t.Helper()
		row, err := q.InsertPendingAttachment(ctx, db.InsertPendingAttachmentParams{ID: uuid.New(), WorkspaceID: ws, UploaderID: u, Name: "upload.zip", ContentType: "application/zip", SizeBytes: 12, Sha256: "h", BlobKey: "k", Kind: kind, InnerFiles: 3, UnpackedBytes: 40})
		if err != nil {
			t.Fatal(err)
		}
		if row.Kind != kind || row.InnerFiles != 3 || row.UnpackedBytes != 40 {
			t.Fatalf("upload metadata: %+v", row)
		}
		return row
	}
	pending, ordinary := insert("service"), insert("file")
	name, start, client := "zeta", "python server.py", "client.py"
	params := db.LinkServiceParams{ID: pending.ID, WorkspaceID: ws, UploaderID: u, CardID: &card, ServiceName: &name, ServiceStart: &start, ServiceClient: &client}
	for _, change := range []func(*db.LinkServiceParams){
		func(p *db.LinkServiceParams) { p.WorkspaceID = uuid.New() },
		func(p *db.LinkServiceParams) { p.UploaderID = uuid.New() },
		func(p *db.LinkServiceParams) { p.ID = uuid.New() },
		func(p *db.LinkServiceParams) { p.ID = ordinary.ID },
	} {
		p := params
		change(&p)
		if n, err := q.LinkService(ctx, p); err != nil || n != 0 {
			t.Fatalf("ineligible LinkService = %d (%v), want 0", n, err)
		}
	}
	incomplete := params
	incomplete.ServiceStart = nil
	if _, err := q.LinkService(ctx, incomplete); err == nil {
		t.Fatal("incomplete service linked")
	}
	var stillPending bool
	if err := env.Owner.QueryRow(ctx, `SELECT card_id IS NULL AND service_name IS NULL AND service_start IS NULL AND service_client IS NULL FROM attachment WHERE id=$1`, pending.ID).Scan(&stillPending); err != nil || !stillPending {
		t.Fatalf("failed link changed pending metadata: %v (%v)", stillPending, err)
	}
	if n, err := q.LinkService(ctx, params); err != nil || n != 1 {
		t.Fatalf("eligible LinkService = %d (%v), want 1", n, err)
	}
	if n, err := q.LinkService(ctx, params); err != nil || n != 0 {
		t.Fatalf("already linked LinkService = %d (%v), want 0", n, err)
	}
	second := insert("service")
	params.ID, name = second.ID, "alpha"
	if n, err := q.LinkService(ctx, params); err != nil || n != 1 {
		t.Fatalf("second LinkService = %d (%v), want 1", n, err)
	}
	if n, err := q.LinkAttachment(ctx, db.LinkAttachmentParams{ID: ordinary.ID, CardID: &card, DiskName: "upload.zip", RecordKey: "c1"}); err != nil || n != 1 {
		t.Fatalf("LinkAttachment = %d (%v), want 1", n, err)
	}
	files, err := q.CardAttachments(ctx, db.CardAttachmentsParams{WorkspaceID: ws, CardID: &card})
	if err != nil || len(files) != 1 || files[0].ID != ordinary.ID {
		t.Fatalf("CardAttachments = %+v (%v), want ordinary file only", files, err)
	}
	services, err := q.CardServices(ctx, db.CardServicesParams{WorkspaceID: ws, CardID: &card})
	if err != nil || len(services) != 2 {
		t.Fatalf("CardServices = %+v (%v), want two services", services, err)
	}
	if *services[0].ServiceName != "alpha" || *services[1].ServiceName != "zeta" {
		t.Fatalf("service order = %+v", services)
	}
	if s := services[1]; s.ServiceStart == nil || *s.ServiceStart != start || s.ServiceClient == nil || *s.ServiceClient != client || s.RecordKey != "" {
		t.Fatalf("linked service metadata = %+v", s)
	}
	services, err = q.CardServices(ctx, db.CardServicesParams{WorkspaceID: uuid.New(), CardID: &card})
	if err != nil || len(services) != 0 {
		t.Fatalf("other workspace services = %+v (%v), want none", services, err)
	}
}
