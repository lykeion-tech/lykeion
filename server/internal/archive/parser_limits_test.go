package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/ulikunitz/xz"
	"hash/crc32"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func dictionaryXZ(t *testing.T, property byte) []byte {
	t.Helper()
	data, err := hex.DecodeString("fd377a585a000004e6d6b446020021011c00000010cf58cce003ff000b5d00006ffdffffa3b77f46320000000c276920976378c300012780080000002e79aeadb1c467fb020000000004595a")
	if err != nil {
		t.Fatal(err)
	}
	data[16] = property
	binary.LittleEndian.PutUint32(data[20:24], crc32.ChecksumIEEE(data[12:20]))
	return data
}

func TestXZDictionaryLimitBeforeAllocation(t *testing.T) {
	data := dictionaryXZ(t, 29) // 96 MiB: a harmless bounded reproduction.
	for _, later := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "later-stream"}[later], func(t *testing.T) {
			input := data
			if later {
				input = append(append(dictionaryXZ(t, 0), 0, 0, 0, 0), data...)
			}
			path := save(t, input)
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, err := List(path, TarXz)
			runtime.ReadMemStats(&after)
			allocated := after.TotalAlloc - before.TotalAlloc
			t.Logf("input=%d allocated=%d error=%v", len(input), allocated, err)
			if !errors.Is(err, ErrUnsafe) {
				t.Errorf("high dictionary accepted: %v", err)
			}
			if allocated > 2<<20 {
				t.Errorf("dictionary allocated before refusal: %d", allocated)
			}
			dest := filepath.Join(t.TempDir(), "out")
			if err := Extract(t.Context(), path, TarXz, dest, 1024, 10); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("extract: %v", err)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("published rejected tree")
			}
			if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
				t.Fatal("left partial tree")
			}
		})
	}
}

func TestZIPDirectoryLimitBeforeAllocation(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for range 260 {
		if _, err := w.CreateHeader(&zip.FileHeader{Name: "same", Method: zip.Store, Comment: strings.Repeat("a", 65535)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := save(t, b.Bytes())
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := List(path, Zip)
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("input=%d allocated=%d error=%v", b.Len(), allocated, err)
	if !errors.Is(err, ErrUnsafe) {
		t.Errorf("metadata-heavy duplicates accepted: %v", err)
	}
	if allocated > 2<<20 {
		t.Errorf("directory allocated before refusal: %d", allocated)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(t.Context(), path, Zip, dest, 0, 1); !errors.Is(err, ErrUnsafe) {
		t.Fatal(err)
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("rejected ZIP left %s", p)
		}
	}
}

func xzEncode(t *testing.T, data []byte, blockSize int64) []byte {
	t.Helper()
	var b bytes.Buffer
	w, err := (xz.WriterConfig{DictCap: 4096, BlockSize: blockSize}).NewWriter(&b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func xzFixtureBlockOffsets(t *testing.T, b []byte) []int {
	t.Helper()
	index := len(b) - 12 - int(binary.LittleEndian.Uint32(b[len(b)-8:len(b)-4])+1)*4
	p := index + 1
	count, n := binary.Uvarint(b[p:])
	p += n
	offsets := make([]int, 0, count)
	at := 12
	for range count {
		size, n := binary.Uvarint(b[p:])
		p += n
		_, n = binary.Uvarint(b[p:])
		p += n
		offsets = append(offsets, at)
		at += int((size + 3) &^ 3)
	}
	return offsets
}

func TestXZAllBlocksAndStreamsAreValidated(t *testing.T) {
	tarPath := tarFile(t, []*tar.Header{{Name: "value", Size: 5, Mode: 0600}}, []string{"final"})
	payload, err := os.ReadFile(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	multi := xzEncode(t, payload, 512)
	offsets := xzFixtureBlockOffsets(t, multi)
	if len(offsets) < 2 {
		t.Fatal("fixture needs multiple blocks")
	}
	t.Run("valid-blocks", func(t *testing.T) {
		path := save(t, multi)
		listing, err := List(path, TarXz)
		if err != nil || listing.Files != 1 || listing.Bytes != 5 {
			t.Fatalf("%+v %v", listing, err)
		}
		dest := filepath.Join(t.TempDir(), "out")
		if err := Extract(t.Context(), path, TarXz, dest, 5, 1); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dest, "value"))
		if err != nil || string(data) != "final" {
			t.Fatalf("%s %v", data, err)
		}
	})
	t.Run("valid-concatenation", func(t *testing.T) {
		data := append(xzEncode(t, payload[:512], 512), 0, 0, 0, 0)
		data = append(data, xzEncode(t, payload[512:], 512)...)
		data = append(data, 0, 0, 0, 0)
		listing, err := List(save(t, data), TarXz)
		if err != nil || listing.Bytes != 5 || listing.Files != 1 {
			t.Fatalf("%+v %v", listing, err)
		}
	})
	t.Run("later-block", func(t *testing.T) {
		data := bytes.Clone(multi)
		start := offsets[1]
		size := (int(data[start]) + 1) * 4
		data[start+4] = 40 // maximal dictionary property, never passed to a decoder.
		binary.LittleEndian.PutUint32(data[start+size-4:], crc32.ChecksumIEEE(data[start:start+size-4]))
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		err := preflightXZ(bytes.NewReader(data), int64(len(data)))
		runtime.ReadMemStats(&after)
		if !errors.Is(err, ErrUnsafe) || after.TotalAlloc-before.TotalAlloc > 2<<20 {
			t.Fatalf("%v allocated=%d", err, after.TotalAlloc-before.TotalAlloc)
		}
	})
	t.Run("forged-index", func(t *testing.T) {
		data := bytes.Clone(multi)
		index := len(data) - 12 - int(binary.LittleEndian.Uint32(data[len(data)-8:len(data)-4])+1)*4
		data[index+2]++
		crcAt := len(data) - 16
		binary.LittleEndian.PutUint32(data[crcAt:], crc32.ChecksumIEEE(data[index:crcAt]))
		if _, err := List(save(t, data), TarXz); !errors.Is(err, ErrUnsafe) {
			t.Fatal(err)
		}
	})
	t.Run("64MiB-allowed", func(t *testing.T) {
		if _, err := List(save(t, dictionaryXZ(t, 28)), TarXz); err != nil {
			t.Fatal(err)
		}
	})
}

// One local file can have repeated directory records. This builds arbitrary
// metadata lengths without writing duplicate payloads or allocating a ZIP index.
func directoryZIP(t *testing.T, length int) []byte {
	t.Helper()
	base, err := os.ReadFile(zipFile(t, "same", 0600, ""))
	if err != nil {
		t.Fatal(err)
	}
	e := len(base) - 22
	start := int(binary.LittleEndian.Uint32(base[e+16:]))
	header := bytes.Clone(base[start : start+46])
	binary.LittleEndian.PutUint16(header[30:], 0)
	data := bytes.Clone(base[:start])
	count := 0
	for left := length; left > 0; {
		span := left
		if span > 65585 {
			span = 65585
		}
		if left-span > 0 && left-span < 50 {
			span -= 50 - (left - span)
		}
		if span < 50 {
			t.Fatal("directory size cannot represent a record")
		}
		binary.LittleEndian.PutUint16(header[32:], uint16(span-50))
		data = append(data, header...)
		data = append(data, []byte("same")...)
		data = append(data, make([]byte, span-50)...)
		left -= span
		count++
	}
	end := bytes.Clone(base[e:])
	binary.LittleEndian.PutUint16(end[8:], uint16(count))
	binary.LittleEndian.PutUint16(end[10:], uint16(count))
	binary.LittleEndian.PutUint32(end[12:], uint32(length))
	return append(data, end...)
}
func zip64Fixture(data []byte) []byte {
	e := len(data) - 22
	end := bytes.Clone(data[e:])
	var z [56]byte
	binary.LittleEndian.PutUint32(z[:], 0x06064b50)
	binary.LittleEndian.PutUint64(z[4:], 44)
	binary.LittleEndian.PutUint16(z[12:], 45)
	binary.LittleEndian.PutUint16(z[14:], 45)
	for _, at := range []int{24, 32} {
		binary.LittleEndian.PutUint64(z[at:], uint64(binary.LittleEndian.Uint16(end[10:])))
	}
	binary.LittleEndian.PutUint64(z[40:], uint64(binary.LittleEndian.Uint32(end[12:])))
	binary.LittleEndian.PutUint64(z[48:], uint64(binary.LittleEndian.Uint32(end[16:])))
	var locator [20]byte
	binary.LittleEndian.PutUint32(locator[:], 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(e))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	binary.LittleEndian.PutUint16(end[8:], 0xffff)
	binary.LittleEndian.PutUint16(end[10:], 0xffff)
	binary.LittleEndian.PutUint32(end[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(end[16:], 0xffffffff)
	result := append(bytes.Clone(data[:e]), z[:]...)
	result = append(result, locator[:]...)
	return append(result, end...)
}
func TestZIPPreflightBoundsForgedMetadata(t *testing.T) {
	small := directoryZIP(t, 100)
	for _, zip64 := range []bool{false, true} {
		data := bytes.Clone(small)
		if zip64 {
			data = zip64Fixture(data)
		}
		t.Run(fmt.Sprintf("valid-zip64=%v", zip64), func(t *testing.T) {
			if _, err := List(save(t, data), Zip); err != nil {
				t.Fatal(err)
			}
		})
		for _, field := range []string{"length", "count", "offset", "underdeclared"} {
			t.Run(fmt.Sprintf("zip64=%v/%s", zip64, field), func(t *testing.T) {
				forged := bytes.Clone(data)
				if zip64 {
					at := len(forged) - 22 - 20 - 56
					switch field {
					case "length":
						binary.LittleEndian.PutUint64(forged[at+40:], maxZIPDirectoryBytes+1)
					case "count":
						binary.LittleEndian.PutUint64(forged[at+32:], 1<<62)
					case "offset":
						binary.LittleEndian.PutUint64(forged[at+48:], 1<<63)
					case "underdeclared":
						binary.LittleEndian.PutUint64(forged[at+40:], 50)
					}
				} else {
					at := len(forged) - 22
					switch field {
					case "length":
						binary.LittleEndian.PutUint32(forged[at+12:], maxZIPDirectoryBytes+1)
					case "count":
						binary.LittleEndian.PutUint16(forged[at+10:], 60000)
					case "offset":
						binary.LittleEndian.PutUint32(forged[at+16:], 0x80000000)
					case "underdeclared":
						binary.LittleEndian.PutUint32(forged[at+12:], 50)
					}
				}
				if _, err := List(save(t, forged), Zip); !errors.Is(err, ErrUnsafe) {
					t.Fatal(err)
				}
			})
		}
	}
	for _, size := range []int{maxZIPDirectoryBytes, maxZIPDirectoryBytes + 1} {
		t.Run(fmt.Sprintf("directory=%d", size), func(t *testing.T) {
			data := directoryZIP(t, size)
			err := preflightZIP(bytes.NewReader(data), int64(len(data)))
			if size == maxZIPDirectoryBytes && err != nil || size > maxZIPDirectoryBytes && !errors.Is(err, ErrUnsafe) {
				t.Fatal(err)
			}
		})
	}
}

func TestZIPDuplicatesBelowMetadataCapKeepLastOccurrence(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i := 0; i < 20050; i++ {
		f, err := w.CreateHeader(&zip.FileHeader{Name: "same", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if i == 20049 {
			if _, err = f.Write([]byte("last")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := save(t, b.Bytes())
	listing, err := List(path, Zip)
	if err != nil || listing.Files != 1 || listing.Bytes != 4 || len(listing.Entries) != 1 || listing.Entries[0].Size != 4 {
		t.Fatalf("%+v %v", listing, err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(t.Context(), path, Zip, dest, 4, 1); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dest, "same"))
	if err != nil || string(content) != "last" {
		t.Fatalf("%s %v", content, err)
	}
}

type cancelingArchiveReader struct {
	context.Context
	cancel context.CancelFunc
	r      *bytes.Reader
	calls  int
}

func (r *cancelingArchiveReader) ReadAt(p []byte, at int64) (int, error) {
	r.calls++
	if r.calls == 4 {
		r.cancel()
	}
	if err := r.Err(); err != nil {
		return 0, err
	}
	return r.r.ReadAt(p, at)
}
func TestArchivePreflightsObserveCancellation(t *testing.T) {
	for _, kind := range []Format{Zip, TarXz} {
		data := dictionaryXZ(t, 0)
		if kind == Zip {
			data = directoryZIP(t, 500)
		}
		ctx, cancel := context.WithCancel(t.Context())
		r := &cancelingArchiveReader{Context: ctx, cancel: cancel, r: bytes.NewReader(data)}
		var err error
		if kind == Zip {
			err = preflightZIP(r, int64(len(data)))
		} else {
			err = preflightXZ(r, int64(len(data)))
		}
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}
