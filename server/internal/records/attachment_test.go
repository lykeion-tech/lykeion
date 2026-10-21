package records

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestExtractText(t *testing.T) {
	pdf, err := os.ReadFile("testdata/finding.pdf")
	if err != nil {
		t.Fatal(err)
	}
	var pic bytes.Buffer
	if err := png.Encode(&pic, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, typ string
		data      []byte
		want      string
		ok        bool
	}{
		{"data.csv", "text/csv", []byte("gene,fold\nPGC1A,2.4\n"), "gene,fold\nPGC1A,2.4\n", true},
		{"notes.md", "text/markdown", []byte("# Notes\nLiver *only*.\n"), "# Notes\nLiver *only*.\n", true},
		{"fit.go", "text/plain; charset=utf-8", []byte("package fit\n"), "package fit\n", true},
		{"finding.pdf", "application/pdf", pdf, "Mitochondrial density rose in fasting mice.", true},
		{"plot.png", "image/png", pic.Bytes(), "", false},
		{"broken.pdf", "application/pdf", []byte("%PDF-1.4\nnot really"), "", false},
		{"latin1.txt", "text/plain", []byte{'c', 'a', 'f', 0xe9}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := ExtractText(tc.name, tc.typ, bytes.NewReader(tc.data))
			if err != nil || ok != tc.ok || (tc.ok && !strings.Contains(got, tc.want)) || (!tc.ok && got != "") {
				t.Fatalf("ExtractText = %q, %v, %v; want %q, %v", got, ok, err, tc.want, tc.ok)
			}
		})
	}
}

func TestDiskNames(t *testing.T) {
	got := DiskNames([]string{"data.csv", "Data.csv", "../../etc/passwd", ".env", "a\\b.txt", "data.csv", "...", "/", "", "data (2).csv",
		strings.Repeat("é", 200) + ".csv"})
	want := []string{"data.csv", "Data (2).csv", "etcpasswd", "env", "ab.txt", "data (3).csv", "file", "file (2)", "file (3)", "data (2) (2).csv"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("name %d = %q, want %q", i, got[i], w)
		}
	}
	long := got[len(got)-1]
	if len(long) > 255 || !strings.HasSuffix(long, ".csv") || !strings.HasPrefix(long, "é") {
		t.Errorf("long name %q (%d bytes)", long, len(long))
	}
}
