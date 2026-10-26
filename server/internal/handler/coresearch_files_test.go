package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/storage"
	db "lykeion/server/pkg/db/generated"
)

func csUpload(t *testing.T, s *server, tok, ws, project, path string, base int, force bool, data []byte) (int, db.CoresearchFileVersion) {
	t.Helper()
	code, v, _ := csUploadResponse(t, s, tok, ws, project, path, base, force, data)
	return code, v
}
func csUploadResponse(t *testing.T, s *server, tok, ws, project, path string, base int, force bool, data []byte) (int, db.CoresearchFileVersion, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("path", path)
	_ = mw.WriteField("base", strconv.Itoa(base))
	_ = mw.WriteField("force", strconv.FormatBool(force))
	f, _ := mw.CreateFormFile("file", "upload")
	_, _ = f.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", s.URL+csProjects+"/"+project+"/files", &body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Workspace-ID", ws)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(resp.Body)
	if e != nil {
		t.Fatal(e)
	}
	var out db.CoresearchFileVersion
	if resp.StatusCode == 201 {
		if e := json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
	}
	return resp.StatusCode, out, raw
}
func TestSavePathRules(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	for _, path := range []string{".claude/settings.json", "../x", ".mcp.json"} {
		if code, _ := csUpload(t, s, a, ws, p.ID, path, 0, false, []byte("x")); code != 400 {
			t.Fatalf("%s: %d", path, code)
		}
	}
	var files []map[string]any
	if code := s.call(t, "GET", csProjects+"/"+p.ID+"/files", a, ws, nil, &files); code != 200 || len(files) != 0 {
		t.Fatalf("forbidden files %d %+v", code, files)
	}
}
func TestStaleSaveRefused(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	for i := 0; i < 2; i++ {
		if c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", i, false, []byte(strconv.Itoa(i))); c != 201 {
			t.Fatal(c)
		}
	}
	code, _, raw := csUploadResponse(t, s, a, ws, p.ID, "a.txt", 1, false, []byte("stale"))
	var stale struct {
		Error         string
		Base, Current int
	}
	if e := json.Unmarshal(raw, &stale); e != nil {
		t.Fatal(e)
	}
	if code != 409 || stale.Error != "stale_base" || stale.Base != 1 || stale.Current != 2 {
		t.Fatalf("stale response %d %s", code, raw)
	}
}
func TestForceNamesOverriddenVersion(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, []byte("1"))
	csUpload(t, s, a, ws, p.ID, "a.txt", 1, false, []byte("2"))
	c, v := csUpload(t, s, a, ws, p.ID, "a.txt", 1, true, []byte("3"))
	if c != 201 || v.N != 3 || v.Overrode == nil || *v.Overrode != 2 {
		t.Fatalf("%d %+v", c, v)
	}
}
func TestRestoreMakesANewVersion(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, v1 := csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, []byte("1"))
	csUpload(t, s, a, ws, p.ID, "a.txt", 1, false, []byte("2"))
	var v db.CoresearchFileVersion
	c := s.call(t, "POST", csProjects+"/"+p.ID+"/files/"+v1.FileID.String()+"/restore", a, ws, map[string]int{"n": 1, "base": 2}, &v)
	if c != 201 || v.N != 3 || v.Sha256 != v1.Sha256 || v.RestoredFrom == nil || *v.RestoredFrom != 1 {
		t.Fatalf("%d %+v", c, v)
	}
}
func TestConcurrentSavesOneWins(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, []byte("1"))
	codes := raceCalls(t, func() int { c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 1, false, []byte("2")); return c }, func() int { c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 1, false, []byte("3")); return c })
	if !(codes[0] == 201 && codes[1] == 409 || codes[0] == 409 && codes[1] == 201) {
		t.Fatal(codes)
	}
}
func TestTextLimit(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	if c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, bytes.Repeat([]byte("a"), (1<<20)+1)); c != 413 {
		t.Fatal(c)
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "a.bin", 0, false, bytes.Repeat([]byte{0}, 2<<20)); c != 201 {
		t.Fatal(c)
	}
}
func TestProjectQuota(t *testing.T) {
	old := coresearch.MaxProjectBytes
	coresearch.MaxProjectBytes = 5
	t.Cleanup(func() { coresearch.MaxProjectBytes = old })
	s, a, _, _, ws, p := csFixture(t)
	if c, _ := csUpload(t, s, a, ws, p.ID, "a", 0, false, []byte("123")); c != 201 {
		t.Fatal(c)
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "b", 0, false, []byte("123")); c != 201 {
		t.Fatalf("dedup %d", c)
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "b", 1, false, []byte("456")); c != 413 {
		t.Fatalf("quota %d", c)
	}
}
func TestProjectQuotaConcurrentDifferentFiles(t *testing.T) {
	old := coresearch.MaxProjectBytes
	coresearch.MaxProjectBytes = 5
	t.Cleanup(func() { coresearch.MaxProjectBytes = old })
	s, a, _, _, ws, p := csFixture(t)
	codes := raceCalls(t, func() int { c, _ := csUpload(t, s, a, ws, p.ID, "a", 0, false, []byte("123")); return c }, func() int { c, _ := csUpload(t, s, a, ws, p.ID, "b", 0, false, []byte("456")); return c })
	if !(codes[0] == 201 && codes[1] == 413 || codes[0] == 413 && codes[1] == 201) {
		t.Fatal(codes)
	}
}
func TestCoresearchDownload(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	for _, name := range []string{"x.txt", "x.png", "x.svg", "x.html"} {
		c, v := csUpload(t, s, a, ws, p.ID, name, 0, false, []byte("content"))
		if c != 201 {
			t.Fatal(c)
		}
		path := csProjects + "/" + p.ID + "/files/" + v.FileID.String() + "/versions/1/content"
		req, _ := http.NewRequest("GET", s.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+a)
		req.Header.Set("X-Workspace-ID", ws)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "content" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("download %d %s", resp.StatusCode, body)
		}
		if (strings.HasSuffix(name, "svg") || strings.HasSuffix(name, "html")) && !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
			t.Fatal("active content inline")
		}
		if c := s.call(t, "GET", path, b, ws, nil, nil); c != 404 {
			t.Fatal(c)
		}
	}
}

func TestCoresearchRefusedUploadCleansOnlyUnreferencedBlob(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	if c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, []byte("head")); c != 201 {
		t.Fatal(c)
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, []byte("refused")); c != 409 {
		t.Fatal(c)
	}
	sum := sha256.Sum256([]byte("refused"))
	key := coresearch.BlobKey(uuid.MustParse(ws), hex.EncodeToString(sum[:]))
	if rc, e := s.H.Blobs.Get(t.Context(), key); !errors.Is(e, storage.ErrNotFound) {
		if rc != nil {
			rc.Close()
		}
		t.Fatalf("refused blob remained: %v", e)
	}
	// B's project is hidden from A. Repairing its missing blob during A's stale
	// save must not remove the committed reference's content during cleanup.
	var hidden csProject
	if c := s.call(t, "POST", csProjects, b, ws, map[string]string{"name": "Hidden"}, &hidden); c != 201 {
		t.Fatal(c)
	}
	_, v := csUpload(t, s, b, ws, hidden.ID, "private.txt", 0, false, []byte("shared"))
	if e := s.H.Blobs.Delete(t.Context(), v.BlobKey); e != nil {
		t.Fatal(e)
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, []byte("shared")); c != 409 {
		t.Fatal(c)
	}
	rc, e := s.H.Blobs.Get(t.Context(), v.BlobKey)
	if e != nil {
		t.Fatalf("hidden project's blob removed: %v", e)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "shared" {
		t.Fatalf("blob %q", got)
	}
}

type coresearchCountingStorage struct {
	storage.Storage
	puts atomic.Int32
}

func (s *coresearchCountingStorage) Put(ctx context.Context, key string, r io.Reader, n int64) error {
	s.puts.Add(1)
	return s.Storage.Put(ctx, key, r, n)
}
func TestCoresearchConcurrentSharedBlobKeptAndDeduplicated(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, []byte("head"))
	blobs := &coresearchCountingStorage{Storage: s.H.Blobs}
	s.H.Blobs = blobs
	codes := raceCalls(t, func() int { c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 1, false, []byte("shared")); return c }, func() int { c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 1, false, []byte("shared")); return c })
	if !(codes[0] == 201 && codes[1] == 409 || codes[0] == 409 && codes[1] == 201) {
		t.Fatal(codes)
	}
	if blobs.puts.Load() != 1 {
		t.Fatalf("equal blob uploaded %d times", blobs.puts.Load())
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "b.txt", 0, false, []byte("shared")); c != 201 {
		t.Fatal(c)
	}
	if blobs.puts.Load() != 1 {
		t.Fatalf("equal blob uploaded %d times", blobs.puts.Load())
	}
}

func TestForceAtHeadRecordsOverriddenVersion(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	if c, _ := csUpload(t, s, a, ws, p.ID, "a.txt", 0, false, []byte("1")); c != 201 {
		t.Fatal(c)
	}
	c, v := csUpload(t, s, a, ws, p.ID, "a.txt", 1, true, []byte("2"))
	if c != 201 || v.Overrode == nil || *v.Overrode != 1 {
		t.Fatalf("forced save %d %+v", c, v)
	}
}

func TestCoresearchUploadAndScopedWriteLockOrder(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	if code := csAdd(t, s, a, b, ws, p.ID, "member"); code != 201 {
		t.Fatal(code)
	}
	blocker, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(t.Context(), `SELECT id FROM coresearch_project WHERE id=$1 FOR UPDATE`, p.ID); err != nil {
		t.Fatal(err)
	}
	// Queue the upload at the project first. Its final transaction must already
	// own the workspace lock before a scoped rename can acquire that lock.
	upload := make(chan int, 1)
	go func() {
		code, _ := csUpload(t, s, b, ws, p.ID, "member.txt", 0, false, []byte("member save"))
		upload <- code
	}()
	uploadPID := waitCoresearchLock(t, s, 0, "LockCoresearchProject")
	rename := make(chan int, 1)
	go func() {
		rename <- s.call(t, "PATCH", csProjects+"/"+p.ID, a, ws, map[string]string{"name": "Renamed"}, nil)
	}()
	waitCoresearchLock(t, s, uploadPID, "")
	if err = blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var uploadCode, renameCode int
	select {
	case uploadCode = <-upload:
	case <-time.After(8 * time.Second):
		t.Fatal("upload did not finish")
	}
	select {
	case renameCode = <-rename:
	case <-time.After(8 * time.Second):
		t.Fatal("rename did not finish")
	}
	if uploadCode != 201 || renameCode != 200 {
		t.Fatalf("competing member upload / owner rename: %d / %d", uploadCode, renameCode)
	}
	var files []coresearchFileView
	if code := s.call(t, "GET", csProjects+"/"+p.ID+"/files", b, ws, nil, &files); code != 200 || len(files) != 1 || files[0].Path != "member.txt" {
		t.Fatalf("member's committed file: %d %+v", code, files)
	}
}

func waitCoresearchLock(t *testing.T, s *server, except int, query string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid int
		err := s.Env.Owner.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND usename='lykeion_app' AND wait_event_type='Lock' AND pid<>$1 AND query LIKE $2 ORDER BY backend_start LIMIT 1`, except, "%"+query+"%").Scan(&pid)
		if err == nil {
			return pid
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("observe transaction lock: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no blocked transaction matching %q: %v", query, ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestRestoreRefusesOldProtectedPaths(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	for _, path := range []string{".CLAUDE/settings.json", "a/.Git/config"} {
		_, v := csUpload(t, s, a, ws, p.ID, "safe.txt", 0, false, []byte("x"))
		if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_file SET path=$1 WHERE id=$2", path, v.FileID); err != nil {
			t.Fatal(err)
		}
		if code := s.call(t, "POST", csProjects+"/"+p.ID+"/files/"+v.FileID.String()+"/restore", a, ws, map[string]int{"n": 1, "base": 1}, nil); code != 400 {
			t.Fatalf("restored %s: %d", path, code)
		}
	}
}
