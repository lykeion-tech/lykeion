package agent

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type skillSwapReader struct {
	io.Reader
	swap func()
	done bool
}

func (r *skillSwapReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		r.swap()
	}
	return r.Reader.Read(p)
}

func TestSkillExtractionStaysAnchoredWhenParentIsReplaced(t *testing.T) {
	for _, parent := range []string{".claude", ".agents"} {
		t.Run(parent, func(t *testing.T) {
			chat, outside := t.TempDir(), t.TempDir()
			rel := filepath.Join(parent, "skills", "lyk-method")
			dst := filepath.Join(chat, rel)
			if err := os.MkdirAll(dst, 0700); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(outside, "skills", "lyk-method")
			if err := os.MkdirAll(victim, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(victim, "SKILL.md"), []byte("outside sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			body := []byte("new managed method")
			if err := tw.WriteHeader(&tar.Header{Name: "SKILL.md", Mode: 0600, Size: int64(len(body))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			reader := &skillSwapReader{Reader: bytes.NewReader(buf.Bytes()), swap: func() {
				if err := os.Rename(filepath.Join(chat, parent), filepath.Join(chat, "retired")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(chat, parent)); err != nil {
					t.Fatal(err)
				}
			}}
			err := Untar(reader, dst)
			data, readErr := os.ReadFile(filepath.Join(victim, "SKILL.md"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(data) != "outside sentinel" {
				t.Fatalf("extraction escaped after parent swap: %q (err=%v)", data, err)
			}
		})
	}
}
