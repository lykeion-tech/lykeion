package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"github.com/ulikunitz/xz"
	"hash"
	"hash/crc32"
	"io"
)

// xzCursor reads framing only, with fixed-size buffers. Payload skips cannot
// escape the input; its ReaderAt also checks cancellation on every read.
type xzCursor struct {
	r        io.ReaderAt
	at, size int64
}

func (c *xzCursor) read(p []byte) error {
	if int64(len(p)) > c.size-c.at {
		return ErrUnsafe
	}
	if _, err := c.r.ReadAt(p, c.at); err != nil {
		return err
	}
	c.at += int64(len(p))
	return nil
}
func (c *xzCursor) skip(n int64) error {
	if n < 0 || n > c.size-c.at {
		return ErrUnsafe
	}
	c.at += n
	return nil
}
func (c *xzCursor) byte() (byte, error) { var p [1]byte; err := c.read(p[:]); return p[0], err }
func (c *xzCursor) vli() (uint64, error) {
	var value uint64
	for i := 0; i < 9; i++ {
		b, err := c.byte()
		if err != nil {
			return 0, err
		}
		value |= uint64(b&127) << uint(7*i)
		if b < 128 {
			if i > 0 && b == 0 {
				return 0, ErrUnsafe
			}
			return value, nil
		}
	}
	return 0, ErrUnsafe
}
func (c *xzCursor) zeros(n int64) error {
	for ; n > 0; n-- {
		b, err := c.byte()
		if err != nil {
			return err
		}
		if b != 0 {
			return ErrUnsafe
		}
	}
	return nil
}

// xzBlocks walks actual chunk framing rather than trusting index offsets.
// Its digest checks the stream index without retaining a record per block.
type xzBlocks struct {
	c       xzCursor
	header  [12]byte
	active  bool
	records uint64
	digest  hash.Hash
	streams int
}
type xzBlock struct {
	header             [12]byte
	start, length      int64
	unpadded, unpacked uint64
}

func xzRecordDigest(h hash.Hash, unpadded, unpacked uint64) {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], unpadded)
	binary.LittleEndian.PutUint64(b[8:], unpacked)
	h.Write(b[:])
}

func (x *xzBlocks) next() (xzBlock, error) {
	var result xzBlock
	c := &x.c
	for {
		if !x.active {
			if c.at == c.size {
				return result, io.EOF
			}
			if err := c.read(x.header[:]); err != nil {
				return result, err
			}
			h := x.header
			if !bytes.Equal(h[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0}) || h[6] != 0 || binary.LittleEndian.Uint32(h[8:]) != crc32.ChecksumIEEE(h[6:8]) {
				return result, ErrUnsafe
			}
			switch h[7] {
			case 0, 1, 4, 10:
			default:
				return result, ErrUnsafe
			}
			x.digest = sha256.New()
			x.records = 0
			x.active = true
			x.streams++
		}
		start := c.at
		first, err := c.byte()
		if err != nil {
			return result, err
		}
		if first == 0 {
			if err := x.finishIndex(start); err != nil {
				return result, err
			}
			continue
		}
		headerSize := (int(first) + 1) * 4
		var block [1024]byte
		block[0] = first
		if err := c.read(block[1:headerSize]); err != nil {
			return result, err
		}
		if binary.LittleEndian.Uint32(block[headerSize-4:]) != crc32.ChecksumIEEE(block[:headerSize-4]) || block[1]&0x3f != 0 {
			return result, ErrUnsafe
		}
		// The decoder supports a single LZMA2 filter only.
		fields := xzCursor{r: bytes.NewReader(block[:headerSize-4]), at: 2, size: int64(headerSize - 4)}
		var compressed, uncompressed uint64
		if block[1]&0x40 != 0 {
			compressed, err = fields.vli()
			if err != nil {
				return result, err
			}
		}
		if block[1]&0x80 != 0 {
			uncompressed, err = fields.vli()
			if err != nil {
				return result, err
			}
		}
		filter, err := fields.vli()
		if err != nil || filter != 0x21 {
			return result, ErrUnsafe
		}
		props, err := fields.vli()
		if err != nil || props != 1 {
			return result, ErrUnsafe
		}
		dict, err := fields.byte()
		if err != nil {
			return result, err
		}
		// Property 28 is exactly 64 MiB; higher properties request more.
		if dict > 28 {
			return result, ErrUnsafe
		}
		if err := fields.zeros(fields.size - fields.at); err != nil {
			return result, err
		}
		dataAt := c.at
		var unpacked uint64
		for {
			control, err := c.byte()
			if err != nil {
				return result, err
			}
			if control == 0 {
				break
			}
			var chunk [5]byte
			var output uint64
			switch {
			case control == 1 || control == 2:
				if err := c.read(chunk[:2]); err != nil {
					return result, err
				}
				output = uint64(binary.BigEndian.Uint16(chunk[:2])) + 1
				if err := c.skip(int64(output)); err != nil {
					return result, err
				}
			case control >= 0x80:
				n := 4
				if control >= 0xc0 {
					n = 5
				}
				if err := c.read(chunk[:n]); err != nil {
					return result, err
				}
				output = (uint64(control&31)<<16 | uint64(binary.BigEndian.Uint16(chunk[:2]))) + 1
				if err := c.skip(int64(binary.BigEndian.Uint16(chunk[2:4])) + 1); err != nil {
					return result, err
				}
			default:
				return result, ErrUnsafe
			}
			if output > uint64(1<<63-1)-unpacked {
				return result, ErrUnsafe
			}
			unpacked += output
		}
		dataSize := c.at - dataAt
		if block[1]&0x40 != 0 && uint64(dataSize) != compressed || block[1]&0x80 != 0 && unpacked != uncompressed {
			return result, ErrUnsafe
		}
		if err := c.zeros((4 - dataSize%4) % 4); err != nil {
			return result, err
		}
		check := int64(0)
		switch x.header[7] {
		case 1:
			check = 4
		case 4:
			check = 8
		case 10:
			check = 32
		}
		if err := c.skip(check); err != nil {
			return result, err
		}
		result = xzBlock{x.header, start, c.at - start, uint64(int64(headerSize) + dataSize + check), unpacked}
		xzRecordDigest(x.digest, result.unpadded, result.unpacked)
		x.records++
		return result, nil
	}
}

func (x *xzBlocks) finishIndex(start int64) error {
	c := &x.c
	records, err := c.vli()
	if err != nil || records != x.records {
		return ErrUnsafe
	}
	digest := sha256.New()
	for range records {
		unpadded, err := c.vli()
		if err != nil {
			return err
		}
		unpacked, err := c.vli()
		if err != nil {
			return err
		}
		xzRecordDigest(digest, unpadded, unpacked)
	}
	if !bytes.Equal(digest.Sum(nil), x.digest.Sum(nil)) {
		return ErrUnsafe
	}
	if err := c.zeros((4 - (c.at-start)%4) % 4); err != nil {
		return err
	}
	crc := crc32.NewIEEE()
	var buf [4096]byte
	if _, err := io.CopyBuffer(crc, io.NewSectionReader(c.r, start, c.at-start), buf[:]); err != nil {
		return err
	}
	var checksum [4]byte
	if err := c.read(checksum[:]); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(checksum[:]) != crc.Sum32() {
		return ErrUnsafe
	}
	indexSize := c.at - start
	var footer [12]byte
	if err := c.read(footer[:]); err != nil {
		return err
	}
	if footer[10] != 'Y' || footer[11] != 'Z' || footer[8] != x.header[6] || footer[9] != x.header[7] || binary.LittleEndian.Uint32(footer[:4]) != crc32.ChecksumIEEE(footer[4:10]) || (int64(binary.LittleEndian.Uint32(footer[4:8]))+1)*4 != indexSize {
		return ErrUnsafe
	}
	for c.at < c.size {
		b, err := c.byte()
		if err != nil {
			return err
		}
		if b != 0 {
			c.at--
			break
		}
		if err := c.zeros(3); err != nil {
			return err
		}
	}
	x.active = false
	return nil
}

// preflightXZ checks every block/stream before any decoder or output exists.
func preflightXZ(r io.ReaderAt, size int64) error {
	blocks := xzBlocks{c: xzCursor{r: r, size: size}}
	for {
		_, err := blocks.next()
		if err == io.EOF {
			if blocks.streams == 0 {
				return ErrUnsafe
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// boundedXZReader gives the upstream decoder one block at a time. Its normal
// stream reader retains every block's index record, so using it for the whole
// input would leave memory proportional to the physical block count. Original
// block checksums and contents are unchanged; the preflight verifies the full
// stream indexes and this reader validates each decoded block's sizes/check.
type boundedXZReader struct {
	blocks  xzBlocks
	current io.Reader
}

func (r *boundedXZReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if r.current == nil {
			block, err := r.blocks.next()
			if err != nil {
				return 0, err
			}
			index := []byte{0, 1}
			index = binary.AppendUvarint(index, block.unpadded)
			index = binary.AppendUvarint(index, block.unpacked)
			for len(index)%4 != 0 {
				index = append(index, 0)
			}
			index = binary.LittleEndian.AppendUint32(index, crc32.ChecksumIEEE(index))
			var footer [12]byte
			binary.LittleEndian.PutUint32(footer[4:8], uint32(len(index)/4-1))
			footer[9] = block.header[7]
			footer[10] = 'Y'
			footer[11] = 'Z'
			binary.LittleEndian.PutUint32(footer[:4], crc32.ChecksumIEEE(footer[4:10]))
			stream := io.MultiReader(bytes.NewReader(block.header[:]), io.NewSectionReader(r.blocks.c.r, block.start, block.length), bytes.NewReader(index), bytes.NewReader(footer[:]))
			r.current, err = (xz.ReaderConfig{SingleStream: true}).NewReader(stream)
			if err != nil {
				return 0, err
			}
		}
		n, err := r.current.Read(p)
		if err == io.EOF {
			r.current = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}
