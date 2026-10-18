package skill

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func zipFixture(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for p, b := range files {
		w, err := z.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestSkillBinaryFileRoundTrips(t *testing.T) {
	binary := []byte{0, 255, 254, 128, 42}
	main := []byte("---\nname: byte-method\ndescription: Preserve bytes\n---\nResearch\n")
	b, err := ParseArchive(zipFixture(t, map[string][]byte{"byte-method/SKILL.md": main, "byte-method/assets/raw.bin": binary, "byte-method/.hidden": []byte("kept")}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Files["assets/raw.bin"], binary) || !bytes.Equal(b.Files["SKILL.md"], main) || string(b.Files[".hidden"]) != "kept" {
		t.Fatal("binary file corrupted or file skipped")
	}
	one, err := Tar(b)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Tar(b)
	if err != nil || !bytes.Equal(one, two) {
		t.Fatal("archive is not deterministic")
	}
	tr := tar.NewReader(bytes.NewReader(one))
	found := false
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		if h.Name == "assets/raw.bin" {
			found = true
			if !bytes.Equal(data, binary) {
				t.Fatal("binary file corrupted")
			}
		}
	}
	if !found {
		t.Fatal("missing binary asset")
	}
}
func TestSkillBundleValidation(t *testing.T) {
	main := []byte("---\nname: safe-method\ndescription: Research\n---\n")
	for _, p := range []string{".", "../escape", "/absolute", "a/../escape", "a\\escape", "C:/escape", "a//b", "a/./b", "nul\x00file"} {
		t.Run(p, func(t *testing.T) {
			if _, err := ParseArchive(zipFixture(t, map[string][]byte{"SKILL.md": main, p: []byte("bad")})); err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
	for _, files := range []map[string][]byte{
		{"SKILL.md": main, "other/SKILL.md": main},
		{"folder/SKILL.md": main, "outside": []byte("lost")},
		{"SKILL.md": main, "large": bytes.Repeat([]byte("x"), 1<<20+1)},
		{"SKILL.md": []byte{255}},
	} {
		if _, err := ParseArchive(zipFixture(t, files)); err == nil {
			t.Fatal("accepted malformed or incomplete bundle")
		}
	}
	files := map[string][]byte{"SKILL.md": main}
	for i := 0; i < 257; i++ {
		files[strings.Repeat("a", i+1)] = []byte("x")
	}
	if err := Validate(Bundle{Name: "safe-method", Files: files}); err == nil {
		t.Fatal("accepted too many files")
	}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for range 2 {
		w, _ := z.Create("SKILL.md")
		w.Write(main)
	}
	z.Close()
	if _, err := ParseArchive(out.Bytes()); err == nil {
		t.Fatal("accepted duplicate path")
	}
}

func TestSkillBundleRejectsFileDirectoryClash(t *testing.T) {
	b := Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: safe-method\n---\n"), "assets": []byte("file"), "assets/a.bin": []byte("child")}}
	if err := Validate(b); err == nil {
		t.Fatal("accepted a file also used as a directory")
	}
}

func TestSkillBundleRejectsAggregateAndLinkEntries(t *testing.T) {
	main := []byte("---\nname: safe-method\n---\n")
	files := map[string][]byte{"SKILL.md": main}
	for i := 0; i < 9; i++ {
		files[strings.Repeat("a", i+1)] = bytes.Repeat([]byte("x"), 1<<20)
	}
	if _, err := ParseArchive(zipFixture(t, files)); err == nil {
		t.Fatal("accepted more than 8MiB supporting bytes")
	}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	w, _ := z.Create("SKILL.md")
	w.Write(main)
	header := &zip.FileHeader{Name: "assets/link"}
	header.SetMode(os.ModeSymlink | 0777)
	w, _ = z.CreateHeader(header)
	w.Write([]byte("../outside"))
	z.Close()
	if _, err := ParseArchive(out.Bytes()); err == nil {
		t.Fatal("accepted symlink entry")
	}
}

func TestSkillArchiveDefersOnlyMissingSourceName(t *testing.T) {
	b, err := ParseArchive(zipFixture(t, map[string][]byte{"SKILL.md": []byte("---\ndescription: Source fallback\n---\nResearch\n")}))
	if err != nil || b.Name != "" {
		t.Fatalf("root archive must leave source fallback to caller: %q %v", b.Name, err)
	}
	if err := Validate(b); err == nil {
		t.Fatal("nameless bundle passed full validation")
	}
	b.Name = "source-method"
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseArchive(zipFixture(t, map[string][]byte{"SKILL.md": []byte("---\nname: Bad-Name\n---\n")})); err == nil {
		t.Fatal("present invalid metadata name accepted")
	}
}
