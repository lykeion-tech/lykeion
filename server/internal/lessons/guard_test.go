package lessons

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeJudge(t *testing.T, choice string) *jev.Client {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": map[string]any{"q": map[string]string{"choice": choice}}})
	}))
	t.Cleanup(s.Close)
	return &jev.Client{BaseURL: s.URL, Model: jev.Model, Key: "fake"}
}

func guardTest(t *testing.T, cites func(*fixture) []string, generalises bool, choice string) {
	f := setup(t)
	w := Writer{Jev: fakeJudge(t, choice)}
	err := f.x.Store.Maintenance(t.Context(), func(tx *store.Tx) error {
		if err := tx.Workspace(t.Context(), f.ws); err != nil {
			return err
		}
		ok, _ := w.Guard(context.Background(), tx, Draft{Kind: "lab", Text: "Supported?", Cites: cites(f), Generalises: generalises})
		if ok {
			t.Fatal("accepted unsupported draft")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestGuardRejectsUnresolvedCite(t *testing.T) {
	guardTest(t, func(*fixture) []string { return []string{"record:" + uuid.NewString()} }, false, "supported")
}
func TestGuardNeedsTwoStudiesToGeneralise(t *testing.T) {
	guardTest(t, func(f *fixture) []string { return []string{"study:" + f.card.String()} }, true, "supported")
}
func TestGuardRejectsWhenJevSaysNotSupported(t *testing.T) {
	guardTest(t, func(f *fixture) []string { return []string{"study:" + f.card.String()} }, false, "not_supported")
}
