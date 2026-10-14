package middleware

import (
	"github.com/google/uuid"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScopeSharesTransactionAndRollsBackErrorResponse(t *testing.T) {
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	ctx := t.Context()
	u, err := st.Global().UpsertUserByEmail(ctx, "scope@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	ws := uuid.New()
	if _, err := env.Owner.Exec(ctx, "INSERT INTO workspace(id,slug,name) VALUES($1,'scope','Before')", ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, "INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')", ws, u.ID); err != nil {
		t.Fatal(err)
	}
	iss := auth.Issuer{Secret: []byte("test")}
	tok, err := (auth.Issuer{Secret: iss.Secret, TTL: time.Hour}).Issue(u.ID, u.Email)
	if err != nil {
		t.Fatal(err)
	}
	code := http.StatusConflict
	handler := Auth(st, iss)(Scope(st, FromHeader)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tx := TxFrom(r.Context())
		if tx == nil {
			t.Error("missing scoped transaction")
			http.Error(w, "missing", 500)
			return
		}
		err := InWorkspace(r.Context(), st, "owner", func(got *store.Tx) error {
			if got != tx {
				t.Error("transaction differs")
			}
			_, err := got.Q.RenameWorkspace(r.Context(), db.RenameWorkspaceParams{ID: ws, Name: "After"})
			return err
		})
		if err != nil {
			t.Error(err)
			http.Error(w, "failed", 500)
			return
		}
		w.Header().Set("X-Test", "kept")
		w.WriteHeader(code)
		w.Write([]byte("body"))
	})))
	for _, status := range []int{409, 200} {
		code = status
		req := httptest.NewRequest("PATCH", "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Workspace-ID", ws.String())
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != status || rr.Header().Get("X-Test") != "kept" || rr.Body.String() != "body" {
			t.Fatalf("response %+v", rr)
		}
		var name string
		if err := env.Owner.QueryRow(ctx, "SELECT name FROM workspace WHERE id=$1", ws).Scan(&name); err != nil {
			t.Fatal(err)
		}
		want := "Before"
		if status == 200 {
			want = "After"
		}
		if name != want {
			t.Fatalf("name=%s want=%s", name, want)
		}
	}
}

func TestScopeRechecksRoleAfterConcurrentDemotion(t *testing.T) {
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	ctx := t.Context()
	u, err := st.Global().UpsertUserByEmail(ctx, "demoted@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	ws := uuid.New()
	if _, err := env.Owner.Exec(ctx, "INSERT INTO workspace(id,slug,name) VALUES($1,'demotion','Before')", ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, "INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')", ws, u.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT id FROM workspace WHERE id=$1 FOR UPDATE", ws); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE member SET role='member' WHERE workspace_id=$1 AND user_id=$2", ws, u.ID); err != nil {
		t.Fatal(err)
	}
	iss := auth.Issuer{Secret: []byte("secret"), TTL: time.Hour}
	tok, err := iss.Issue(u.ID, u.Email)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := Auth(st, iss)(Scope(st, FromHeader)(RequireOwner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))))
	req := httptest.NewRequest("DELETE", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Workspace-ID", ws.String())
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(rr, req) }()
	select {
	case <-done:
		t.Fatal("request completed while mutation lock held")
	case <-time.After(30 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request stuck")
	}
	if called || rr.Code != 403 {
		t.Fatalf("handler called=%v status=%d", called, rr.Code)
	}
}
