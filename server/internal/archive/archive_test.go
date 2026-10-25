package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func tarFile(t *testing.T, headers []*tar.Header, contents []string) string {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for i, h := range headers {
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, contents[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return save(t, b.Bytes())
}
func save(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func zipFile(t *testing.T, name string, mode os.FileMode, contents string) string {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	f, err := w.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(f, contents); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return save(t, b.Bytes())
}
func TestArchiveRefusesUnsafeEntries(t *testing.T) {
	cases := []struct {
		name string
		kind byte
		link string
	}{
		{"../x", tar.TypeReg, ""}, {"/x", tar.TypeReg, ""}, {"a/../x", tar.TypeReg, ""},
		{`C:\x`, tar.TypeReg, ""}, {`a\..\x`, tar.TypeReg, ""},
		{"sym", tar.TypeSymlink, "../../outside"}, {"hard", tar.TypeLink, "../../outside"},
		{"fifo", tar.TypeFifo, ""}, {"device", tar.TypeChar, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name+string(tt.kind), func(t *testing.T) {
			p := tarFile(t, []*tar.Header{{Name: tt.name, Typeflag: tt.kind, Linkname: tt.link}}, []string{""})
			if _, err := List(p, Tar); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("List: %v", err)
			}
			dest := filepath.Join(t.TempDir(), "out")
			err := Extract(t.Context(), p, Tar, dest, 1<<20, 100)
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("Extract: %v", err)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("destination survives: %v", err)
			}
			if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
				t.Fatalf("partial survives: %v", err)
			}
		})
	}
	for _, name := range []string{"../x", "/x", "sym"} {
		t.Run("zip-"+name, func(t *testing.T) {
			mode := os.FileMode(0600)
			if name == "sym" {
				mode |= os.ModeSymlink
			}
			p := zipFile(t, name, mode, "outside")
			if _, err := List(p, Zip); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("List: %v", err)
			}
			dest := filepath.Join(t.TempDir(), "out")
			if err := Extract(t.Context(), p, Zip, dest, 100, 100); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("Extract: %v", err)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("destination survives: %v", err)
			}
		})
	}
}
func TestArchiveListingIncludesInnerPathsAndSizes(t *testing.T) {
	p := tarFile(t, []*tar.Header{{Name: "nested/", Typeflag: tar.TypeDir}, {Name: "nested/a.txt", Size: 3, Mode: 0600}, {Name: "b.txt", Size: 2, Mode: 0600}}, []string{"", "abc", "de"})
	got, err := List(p, Tar)
	want := Listing{Files: 3, Bytes: 5, Entries: []Entry{{Path: "nested/a.txt", Size: 3}, {Path: "b.txt", Size: 2}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
}
func TestArchiveExtractsNestedTree(t *testing.T) {
	for _, f := range []Format{Tar, Zip} {
		t.Run(string(f), func(t *testing.T) {
			p := tarFile(t, []*tar.Header{{Name: "nested/a.txt", Size: 3, Mode: 0777}}, []string{"abc"})
			if f == Zip {
				p = zipFile(t, "nested/a.txt", 0777, "abc")
			}
			dest := filepath.Join(t.TempDir(), "out")
			if err := Extract(t.Context(), p, f, dest, 100, 10); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(dest, "nested/a.txt"))
			if err != nil || string(b) != "abc" {
				t.Fatalf("%q %v", b, err)
			}
		})
	}
}
func TestArchiveStopsAtMaxBytes(t *testing.T) {
	p := zipFile(t, "zeros", 0600, string(make([]byte, 2<<20)))
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(t.Context(), p, Zip, dest, 1<<20, 100); !errors.Is(err, ErrTooBig) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("destination survives: %v", err)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("partial survives: %v", err)
	}
}
func TestArchiveKindByMagicNotName(t *testing.T) {
	p := zipFile(t, "a", 0600, "x")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := Kind(b, "notes.txt"); got != Zip {
		t.Fatalf("%v", got)
	}
	if got := Kind([]byte("ordinary text"), "archive.zip"); got != None {
		t.Fatalf("%v", got)
	}
}
func TestArchiveCancelledExtractionLeavesNothing(t *testing.T) {
	p := zipFile(t, "a", 0600, "x")
	dest := filepath.Join(t.TempDir(), "out")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Extract(ctx, p, Zip, dest, 100, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("destination survives: %v", err)
	}
}
func TestArchiveRefusesUndeclaredTarPayload(t *testing.T) {
	p := tarFile(t, []*tar.Header{{Name: "a", Size: 10, Mode: 0600}}, []string{"0123456789"})
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	copy(b[522:532], []byte("undeclared"))
	p = save(t, b)
	if _, err := List(p, Tar); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("List: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(t.Context(), p, Tar, dest, 100, 100); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("destination survives: %v", err)
	}
}

func TestArchiveCompressionFormats(t *testing.T) {
	tarPath := tarFile(t, []*tar.Header{{Name: "nested/a.txt", Size: 3, Mode: 0600}}, []string{"abc"})
	plain, err := os.ReadFile(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []Format{TarGz, TarXz, TarBz2, TarZst} {
		t.Run(string(f), func(t *testing.T) {
			var b bytes.Buffer
			var w io.WriteCloser
			switch f {
			case TarGz:
				w = gzip.NewWriter(&b)
			case TarXz:
				w, err = xz.NewWriter(&b)
			case TarZst:
				w, err = zstd.NewWriter(&b)
			case TarBz2:
				data, e := os.ReadFile("testdata/nested.tar.bz2")
				if e != nil {
					t.Fatal(e)
				}
				b.Write(data)
			}
			if err != nil {
				t.Fatal(err)
			}
			if w != nil {
				if _, err = w.Write(plain); err != nil {
					t.Fatal(err)
				}
				if err = w.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if got := Kind(b.Bytes(), "data.txt"); got != f {
				t.Fatalf("Kind %v, want %v", got, f)
			}
			p := save(t, b.Bytes())
			listing, err := List(p, f)
			if err != nil || listing.Files != 2 || listing.Bytes != 3 {
				t.Fatalf("List %+v %v", listing, err)
			}
			dest := filepath.Join(t.TempDir(), "out")
			if err := Extract(t.Context(), p, f, dest, 100, 10); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dest, "nested/a.txt"))
			if err != nil || string(data) != "abc" {
				t.Fatalf("%q %v", data, err)
			}
		})
	}
}
func TestArchiveSupportsExtendedTarNames(t *testing.T) {
	name := strings.Repeat("long/", 60) + "a.txt"
	for _, format := range []tar.Format{tar.FormatPAX, tar.FormatGNU} {
		t.Run(format.String(), func(t *testing.T) {
			p := tarFile(t, []*tar.Header{{Name: name, Size: 3, Mode: 0600, Format: format}}, []string{"abc"})
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if got := Kind(b[:512], "data.txt"); got != Tar {
				t.Fatalf("Kind %v", got)
			}
			listing, err := List(p, Tar)
			if err != nil || listing.Files != 61 || listing.Entries[0].Path != name {
				t.Fatalf("%+v %v", listing, err)
			}
			if err := Extract(t.Context(), p, Tar, filepath.Join(t.TempDir(), "out"), 100, 100); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestArchiveFailureAfterWritingCleansPartials(t *testing.T) {
	p := tarFile(t, []*tar.Header{{Name: "ok", Size: 3}, {Name: "../bad", Size: 3}}, []string{"abc", "bad"})
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(t.Context(), p, Tar, dest, 100, 10); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("%v", err)
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("partial survives: %s %v", p, err)
		}
	}
}

func TestArchiveStopsAtDeclaredZipSize(t *testing.T) {
	p := zipFile(t, "a", 0600, strings.Repeat("x", 2<<20))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(b, []byte{'P', 'K', 1, 2})
	if at < 0 {
		t.Fatal("no central directory")
	}
	binary.LittleEndian.PutUint32(b[at+24:], 10)
	p = save(t, b)
	dest := filepath.Join(t.TempDir(), "out")
	err = Extract(t.Context(), p, Zip, dest, 1<<20, 100)
	if !errors.Is(err, ErrTooBig) && !errors.Is(err, ErrUnsafe) {
		t.Fatalf("lying size: %v", err)
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("partial survives: %s %v", p, err)
		}
	}
}

func TestArchiveAllowsGlobalPAXMetadata(t *testing.T) {
	p := tarFile(t, []*tar.Header{{Name: "global", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "metadata"}}, {Name: "a", Size: 3, Mode: 0600}}, []string{"", "abc"})
	listing, err := List(p, Tar)
	if err != nil || listing.Files != 1 || listing.Entries[0].Path != "a" {
		t.Fatalf("%+v %v", listing, err)
	}
	if err := Extract(t.Context(), p, Tar, filepath.Join(t.TempDir(), "out"), 100, 100); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveHonorsPAXSizeOverride(t *testing.T) {
	for _, tt := range []struct {
		raw, actual    int
		corruptPadding bool
	}{{0, 512, false}, {1, 513, false}, {1024, 513, false}, {512, 0, false}, {0, 10, true}} {
		t.Run(fmt.Sprintf("%d-to-%d/padding-corrupt-%t", tt.raw, tt.actual, tt.corruptPadding), func(t *testing.T) {
			payload := strings.Repeat("x", tt.actual)
			record := fmt.Sprintf("size=%d\n", tt.actual)
			for n := len(record) + 2; ; n++ {
				candidate := fmt.Sprintf("%d %s", n, record)
				if len(candidate) == n {
					record = candidate
					break
				}
			}
			headerPath := tarFile(t, []*tar.Header{{Name: "pax", Size: int64(len(record)), Mode: 0600}}, []string{record})
			headerBytes, err := os.ReadFile(headerPath)
			if err != nil {
				t.Fatal(err)
			}
			extended := append([]byte(nil), headerBytes[:512]...)
			extended[156] = tar.TypeXHeader
			for i := 148; i < 156; i++ {
				extended[i] = ' '
			}
			checksum := 0
			for _, v := range extended {
				checksum += int(v)
			}
			copy(extended[148:156], fmt.Sprintf("%06o\x00 ", checksum))
			filePath := tarFile(t, []*tar.Header{{Name: "a", Size: int64(tt.raw), Mode: 0600}}, []string{strings.Repeat("r", tt.raw)})
			fileHeader, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatal(err)
			}
			afterPath := tarFile(t, []*tar.Header{{Name: "after", Size: 3, Mode: 0600}}, []string{"end"})
			after, err := os.ReadFile(afterPath)
			if err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			b.Write(extended)
			b.WriteString(record)
			b.Write(make([]byte, (512-len(record)%512)%512))
			b.Write(fileHeader[:512])
			b.WriteString(payload)
			padding := make([]byte, (512-tt.actual%512)%512)
			if tt.corruptPadding {
				copy(padding, []byte("undeclared"))
			}
			b.Write(padding)
			b.Write(after)
			tr := tar.NewReader(bytes.NewReader(b.Bytes()))
			h, err := tr.Next()
			if err != nil || h.Name != "a" || h.Size != int64(tt.actual) {
				t.Fatalf("stdlib fixture header: %+v %v", h, err)
			}
			got, err := io.ReadAll(tr)
			if err != nil || string(got) != payload {
				t.Fatalf("stdlib fixture payload: %d %v", len(got), err)
			}
			h, err = tr.Next()
			if err != nil || h.Name != "after" || h.Size != 3 {
				t.Fatalf("stdlib following header: %+v %v", h, err)
			}
			p := save(t, b.Bytes())
			listing, err := List(p, Tar)
			if tt.corruptPadding {
				if !errors.Is(err, ErrUnsafe) {
					t.Fatalf("List accepted nonzero PAX padding: %v", err)
				}
				dest := filepath.Join(t.TempDir(), "out")
				if err := Extract(t.Context(), p, Tar, dest, 4096, 10); !errors.Is(err, ErrUnsafe) {
					t.Fatalf("Extract accepted nonzero PAX padding: %v", err)
				}
				for _, name := range []string{dest, dest + ".part"} {
					if _, err := os.Stat(name); !os.IsNotExist(err) {
						t.Fatalf("partial survives: %s %v", name, err)
					}
				}
				return
			}
			if err != nil || !reflect.DeepEqual(listing, Listing{Files: 2, Bytes: int64(tt.actual + 3), Entries: []Entry{{Path: "a", Size: int64(tt.actual)}, {Path: "after", Size: 3}}}) {
				t.Fatalf("List: %+v %v", listing, err)
			}
			dest := filepath.Join(t.TempDir(), "out")
			if err := Extract(t.Context(), p, Tar, dest, 4096, 10); err != nil {
				t.Fatal(err)
			}
			got, err = os.ReadFile(filepath.Join(dest, "a"))
			if err != nil || string(got) != payload {
				t.Fatalf("extracted payload: %d %v", len(got), err)
			}
			got, err = os.ReadFile(filepath.Join(dest, "after"))
			if err != nil || string(got) != "end" {
				t.Fatalf("following payload: %q %v", got, err)
			}
		})
	}
}

func directoryArchive(t *testing.T, f Format, n int) string {
	t.Helper()
	var b bytes.Buffer
	if f == Tar {
		w := tar.NewWriter(&b)
		for i := 0; i < n; i++ {
			if err := w.WriteHeader(&tar.Header{Name: fmt.Sprintf("directory-%d/", i), Typeflag: tar.TypeDir, Mode: 0700}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		w := zip.NewWriter(&b)
		for i := 0; i < n; i++ {
			h := &zip.FileHeader{Name: fmt.Sprintf("directory-%d/", i)}
			h.SetMode(os.ModeDir | 0700)
			if _, err := w.CreateHeader(h); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return save(t, b.Bytes())
}
func TestArchiveDirectoriesCountTowardTheLimit(t *testing.T) {
	for _, f := range []Format{Tar, Zip} {
		t.Run(string(f), func(t *testing.T) {
			p := directoryArchive(t, f, 20001)
			if _, err := List(p, f); !errors.Is(err, ErrTooBig) {
				t.Fatalf("directory listing: %v", err)
			}
			dest := filepath.Join(t.TempDir(), "out")
			if err := Extract(t.Context(), p, f, dest, 0, 20000); !errors.Is(err, ErrTooBig) {
				t.Fatalf("directory extraction: %v", err)
			}
			for _, p := range []string{dest, dest + ".part"} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Fatalf("partial survives: %s %v", p, err)
				}
			}
			empty := directoryArchive(t, f, 0)
			if err := Extract(t.Context(), empty, f, filepath.Join(t.TempDir(), "empty"), 0, 0); err != nil {
				t.Fatalf("empty archive: %v", err)
			}
			one := directoryArchive(t, f, 1)
			dest = filepath.Join(t.TempDir(), "one")
			if err := Extract(t.Context(), one, f, dest, 0, 0); !errors.Is(err, ErrTooBig) {
				t.Fatalf("zero entry budget: %v", err)
			}
			for _, p := range []string{dest, dest + ".part"} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Fatalf("partial survives: %s %v", p, err)
				}
			}
		})
	}
}
func TestArchiveImpliedParentsCountBeforeCreation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		budget int
		dir    bool
	}{
		{"a/b/c.txt", 2, false},
		{strings.Repeat("x", 300) + "/file", 1, false},
		{strings.Repeat("x", 300) + "/", 0, true},
	} {
		t.Run(fmt.Sprintf("%d-%t", tt.budget, tt.dir), func(t *testing.T) {
			h := &tar.Header{Name: tt.name, Size: 1, Mode: 0600}
			payload := "x"
			if tt.dir {
				h.Typeflag = tar.TypeDir
				h.Size = 0
				payload = ""
			}
			p := tarFile(t, []*tar.Header{h}, []string{payload})
			listing, err := List(p, Tar)
			want := tt.budget + 1
			if err != nil || listing.Files != want {
				t.Fatalf("List: %+v %v, want %d entries", listing, err, want)
			}
			dest := filepath.Join(t.TempDir(), "out")
			if err := Extract(t.Context(), p, Tar, dest, 1, tt.budget); !errors.Is(err, ErrTooBig) {
				t.Fatalf("entry budget before filesystem creation: %v", err)
			}
			for _, p := range []string{dest, dest + ".part"} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Fatalf("partial survives: %s %v", p, err)
				}
			}
		})
	}
}
func TestArchiveCountsNormalizedPathsOnce(t *testing.T) {
	p := tarFile(t, []*tar.Header{
		{Name: "./", Typeflag: tar.TypeDir}, {Name: "a//", Typeflag: tar.TypeDir}, {Name: "./a/", Typeflag: tar.TypeDir},
		{Name: "a/b.txt", Size: 3}, {Name: "./a//b.txt", Size: 3}, {Name: "a/c/", Typeflag: tar.TypeDir},
		{Name: "./a/c/", Typeflag: tar.TypeDir}, {Name: "a/c/d.txt", Size: 3},
	}, []string{"", "", "", "one", "two", "", "", "end"})
	listing, err := List(p, Tar)
	want := Listing{Files: 4, Bytes: 9, Entries: []Entry{{Path: "a/b.txt", Size: 3}, {Path: "a/c/d.txt", Size: 3}}}
	if err != nil || !reflect.DeepEqual(listing, want) {
		t.Fatalf("List %+v %v, want %+v", listing, err, want)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(t.Context(), p, Tar, dest, 9, 4); err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := filepath.WalkDir(dest, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dest {
			n++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != listing.Files {
		t.Fatalf("extracted %d entries, listing counted %d", n, listing.Files)
	}
	b, err := os.ReadFile(filepath.Join(dest, "a/b.txt"))
	if err != nil || string(b) != "two" {
		t.Fatalf("repeated normalized file: %q %v", b, err)
	}
}
func TestArchiveEntryBudgetFailureCleansEarlierFiles(t *testing.T) {
	p := tarFile(t, []*tar.Header{{Name: "ok", Size: 1}, {Name: "later/dir/", Typeflag: tar.TypeDir}}, []string{"x", ""})
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(t.Context(), p, Tar, dest, 1, 1); !errors.Is(err, ErrTooBig) {
		t.Fatalf("%v", err)
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("partial survives: %s %v", p, err)
		}
	}
}

func TestArchiveManifestKeepsOnlyFinalFiles(t *testing.T) {
	t.Run("many zero-byte duplicates", func(t *testing.T) {
		file, err := os.CreateTemp(t.TempDir(), "repeated-*.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(file)
		w := tar.NewWriter(gz)
		for i := 0; i < 30000; i++ {
			name := "repeated"
			if i%2 == 0 {
				name = "./repeated"
			}
			if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		listing, err := List(file.Name(), TarGz)
		want := Listing{Files: 1, Bytes: 0, Entries: []Entry{{Path: "repeated", Size: 0}}}
		if err != nil || !reflect.DeepEqual(listing, want) {
			t.Fatalf("List: Files=%d Bytes=%d Entries=%d error=%v", listing.Files, listing.Bytes, len(listing.Entries), err)
		}
		dest := filepath.Join(t.TempDir(), "out")
		if err := Extract(t.Context(), file.Name(), TarGz, dest, 0, 1); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(dest, "repeated"))
		if err != nil || info.Size() != 0 {
			t.Fatalf("final empty file: %+v %v", info, err)
		}
		children, err := os.ReadDir(dest)
		if err != nil || len(children) != 1 {
			t.Fatalf("final tree: %d children %v", len(children), err)
		}
	})
	t.Run("last occurrence order and write budget", func(t *testing.T) {
		p := tarFile(t, []*tar.Header{{Name: "./nested/a.txt", Size: 3}, {Name: "middle.txt", Size: 4}, {Name: "nested//a.txt", Size: 11}, {Name: "end.txt", Size: 4}, {Name: "./middle.txt", Size: 1}}, []string{"old", "keep", "replacement", "tail", "z"})
		listing, err := List(p, Tar)
		want := Listing{Files: 4, Bytes: 23, Entries: []Entry{{Path: "nested/a.txt", Size: 11}, {Path: "end.txt", Size: 4}, {Path: "middle.txt", Size: 1}}}
		if err != nil || !reflect.DeepEqual(listing, want) {
			t.Fatalf("List %+v %v, want %+v", listing, err, want)
		}
		dest := filepath.Join(t.TempDir(), "out")
		if err := Extract(t.Context(), p, Tar, dest, listing.Bytes, listing.Files); err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]string{"nested/a.txt": "replacement", "end.txt": "tail", "middle.txt": "z"} {
			b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
			if err != nil || string(b) != want {
				t.Fatalf("%s: %q %v", name, b, err)
			}
		}
		var finalBytes int64
		for _, entry := range listing.Entries {
			finalBytes += entry.Size
		}
		dest = filepath.Join(t.TempDir(), "insufficient")
		if err := Extract(t.Context(), p, Tar, dest, finalBytes, listing.Files); !errors.Is(err, ErrTooBig) {
			t.Fatalf("overwrite payloads must count toward byte budget: %v", err)
		}
		for _, p := range []string{dest, dest + ".part"} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("partial survives: %s %v", p, err)
			}
		}
	})
}

type cancelListingAfterChecks struct {
	context.Context
	cancel context.CancelFunc
	checks int
	limit  int
}

func (c *cancelListingAfterChecks) Err() error {
	c.checks++
	if c.checks == c.limit {
		c.cancel()
	}
	return c.Context.Err()
}
func TestArchiveListContextCancellation(t *testing.T) {
	t.Run("before opening", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := ListContext(ctx, filepath.Join(t.TempDir(), "absent"), Tar); !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-cancel: %v", err)
		}
	})
	t.Run("while skipping PAX headers", func(t *testing.T) {
		record := "13 comment=x\n"
		p := tarFile(t, []*tar.Header{{Name: "pax", Size: int64(len(record)), Mode: 0600}}, []string{record})
		block, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		block = block[:1024]
		block[156] = tar.TypeXHeader
		for i := 148; i < 156; i++ {
			block[i] = ' '
		}
		sum := 0
		for _, v := range block[:512] {
			sum += int(v)
		}
		copy(block[148:156], fmt.Sprintf("%06o\x00 ", sum))
		end := tarFile(t, []*tar.Header{{Name: "final", Mode: 0600}}, []string{""})
		ending, err := os.ReadFile(end)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.CreateTemp(t.TempDir(), "metadata-*.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		for i := 0; i < 10000; i++ {
			if _, err := gz.Write(block); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := gz.Write(ending); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		bounded := &cancelListingAfterChecks{Context: ctx, cancel: cancel, limit: 64}
		listing, err := ListContext(bounded, f.Name(), TarGz)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("mid-scan cancellation: %+v %v, checks %d", listing, err, bounded.checks)
		}
		if listing.Files != 0 {
			t.Fatalf("scanned through metadata before stopping: %+v", listing)
		}
		if bounded.checks > 70 {
			t.Fatalf("continued checking after cancellation: %d", bounded.checks)
		}
	})
}
