package handler

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"

	"lykeion/server/internal/skill"
)

// Hosted availability is opt-in; deterministic imports run in the regular suite.
// The imported folders are data in disposable blob storage, never executed.
func TestLivePublishedSkillImports(t *testing.T) {
	if os.Getenv("LYK_LIVE_SKILL_IMPORTS") != "1" {
		t.Skip("live public hosted-source acceptance is opt-in")
	}
	t.Setenv("GITHUB_TOKEN", "")
	for _, source := range []struct{ name, url, license string }{
		{"github", "https://github.com/anthropics/skills/tree/main/skills/algorithmic-art", "Apache-2.0"},
		{"skills.sh", "https://skills.sh/anthropics/skills/algorithmic-art", "Apache-2.0"},
		{"clawhub", "https://clawhub.ai/douglarek/clawhub-wrapper", "MIT-0"},
	} {
		t.Run(source.name, func(t *testing.T) {
			s := newServer(t)
			token := s.signIn(t, "live-import@lab.test")
			ws := s.workspace(t, token, "public-source-acceptance")
			var got struct {
				skill.Detail
				Error string `json:"error"`
			}
			code := s.call(t, "POST", "/api/skills/import", token, ws.ID, map[string]string{"url": source.url}, &got)
			if code != 201 {
				t.Fatalf("source=%s HTTP=%d error=%s", source.url, code, got.Error)
			}
			if got.Source.Location != source.url || got.Domain != "Workspace" || len(got.Files) == 0 {
				t.Fatal("missing source/folder metadata")
			}
			t.Logf("source=%s license=%s name=%s files=%d", source.url, source.license, got.Name, len(got.Files))
			for _, file := range got.Files {
				req, err := http.NewRequestWithContext(t.Context(), "GET", s.URL+"/api/skills/"+got.ID+"/files?path="+url.QueryEscape(file.Path), nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-Workspace-ID", ws.ID)
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				hash := fmt.Sprintf("%x", sha256.Sum256(data))
				if response.StatusCode != 200 || hash != file.SHA256 || int64(len(data)) != file.Size {
					t.Fatalf("stored file verification failed: %s", file.Path)
				}
				t.Logf("path=%s bytes=%d sha256=%s binary=%t", file.Path, file.Size, file.SHA256, file.Binary)
			}
		})
	}
}
