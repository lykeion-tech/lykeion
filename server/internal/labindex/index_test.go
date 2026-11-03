package labindex

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"lykeion/server/internal/embed"
	"lykeion/server/internal/labmemory"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Own isolated Redis containers only; never use shared preview/test databases.
func redisForTest(t *testing.T, image string) *redis.Client {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker unavailable")
	}
	name := "lyk-ari-task3-" + uuid.NewString()
	out, err := exec.Command("docker", "run", "--rm", "-d", "--name", name, "-p", "127.0.0.1::6379", image).CombinedOutput()
	if err != nil {
		t.Fatalf("start isolated Redis: %s %v", out, err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	out, err = exec.Command("docker", "port", name, "6379/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	r := redis.NewClient(&redis.Options{Addr: strings.TrimSpace(string(out))})
	t.Cleanup(func() { r.Close() })
	ctx := context.Background()
	for n := 0; n < 50; n++ {
		if r.Ping(ctx).Err() == nil {
			return r
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Redis did not start")
	return nil
}
func TestSearchStaysInItsWorkspaceAndFindsNearestFirst(t *testing.T) {
	if os.Getenv("LYK_INDEX_INTEGRATION") != "1" {
		t.Skip("set LYK_INDEX_INTEGRATION=1 to run isolated Redis containers")
	}
	ctx := context.Background()
	r := redisForTest(t, "redis:8-alpine")
	idx := New(r, embed.Fake{}, embed.Model)
	if err := idx.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := idx.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := uuid.New(), uuid.New()
	near, far, foreign := labmemory.Ref{Kind: "study", ID: uuid.New()}, labmemory.Ref{Kind: "study", ID: uuid.New()}, labmemory.Ref{Kind: "study", ID: uuid.New()}
	for _, entry := range []struct {
		ws   uuid.UUID
		r    labmemory.Ref
		text string
	}{{a, near, "target"}, {a, far, "other"}, {b, foreign, "target"}} {
		if err := idx.Put(ctx, entry.ws, entry.r, "v1", entry.text); err != nil {
			t.Fatal(err)
		}
	}
	got, err := idx.Search(ctx, a, "target", 10)
	if err != nil || len(got) != 2 || got[0] != near || got[1] != far {
		t.Fatalf("got %v err %v", got, err)
	}
	fields, err := r.HGetAll(ctx, key(a, near)).Result()
	if err != nil || fields["version"] != "v1" || len(fields) != 6 {
		t.Fatalf("unexpected index fields %v %v", fields, err)
	}
	changed := New(r, embed.Fake{}, "different-model")
	got, err = changed.Search(ctx, a, "target", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("old model results %v %v", got, err)
	}
	if err := idx.Delete(ctx, a, near); err != nil {
		t.Fatal(err)
	}
	got, err = idx.Search(ctx, a, "target", 10)
	if err != nil || len(got) != 1 || got[0] != far {
		t.Fatalf("delete failed %v %v", got, err)
	}
}
func TestEnsureUnavailableOnRedisWithoutSearch(t *testing.T) {
	if os.Getenv("LYK_INDEX_INTEGRATION") != "1" {
		t.Skip("set LYK_INDEX_INTEGRATION=1")
	}
	idx := New(redisForTest(t, "redis:7-alpine"), embed.Fake{}, embed.Model)
	if err := idx.Ensure(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestPutRequiresVersion(t *testing.T) {
	if err := New(nil, embed.Fake{}, embed.Model).Put(context.Background(), uuid.New(), labmemory.Ref{}, "", "text"); err == nil {
		t.Fatal("missing version accepted")
	}
}
func TestVectorEncoding(t *testing.T) {
	v := make([]float32, embed.Dimensions)
	v[0] = 1
	b, err := vector(v)
	if err != nil || len(b) != 1536 || b[2] != 128 || b[3] != 63 {
		t.Fatalf("invalid float32 representation %v", err)
	}
}
