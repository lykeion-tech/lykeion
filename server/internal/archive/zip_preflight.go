package archive

import (
	"encoding/binary"
	"io"
)

const maxZIPDirectoryBytes = 16 << 20

// preflightZIP bounds both the declared allocation hint and the directory the
// standard reader actually walks. That reader may ignore the declared size
// and use an earlier absolute offset, so neither field alone is a safe bound.
func preflightZIP(r io.ReaderAt, size int64) error {
	if size < 22 {
		return ErrUnsafe
	}
	n := int64(65 * 1024)
	if n > size {
		n = size
	}
	tail := make([]byte, int(n))
	if _, err := r.ReadAt(tail, size-n); err != nil {
		return err
	}
	end := -1
	for i := len(tail) - 22; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) == 0x06054b50 {
			if i+22+int(binary.LittleEndian.Uint16(tail[i+20:])) > len(tail) {
				return ErrUnsafe
			}
			end = i
			break
		}
	}
	if end < 0 {
		return ErrUnsafe
	}
	e := tail[end:]
	endAt := size - n + int64(end)
	records := uint64(binary.LittleEndian.Uint16(e[10:]))
	length := uint64(binary.LittleEndian.Uint32(e[12:]))
	offset := uint64(binary.LittleEndian.Uint32(e[16:]))
	// Match the reader's ZIP64 selection, including its legacy size sentinel.
	if records == 0xffff || length == 0xffff || offset == 0xffffffff {
		var locator [20]byte
		if endAt >= 20 {
			if _, err := r.ReadAt(locator[:], endAt-20); err != nil {
				return err
			}
			if binary.LittleEndian.Uint32(locator[:]) == 0x07064b50 && binary.LittleEndian.Uint32(locator[4:]) == 0 && binary.LittleEndian.Uint32(locator[16:]) == 1 {
				pos := binary.LittleEndian.Uint64(locator[8:])
				if pos > uint64(endAt-20) || uint64(endAt-20)-pos < 56 {
					return ErrUnsafe
				}
				var z [56]byte
				if _, err := r.ReadAt(z[:], int64(pos)); err != nil {
					return err
				}
				if binary.LittleEndian.Uint32(z[:]) != 0x06064b50 {
					return ErrUnsafe
				}
				recordSize := binary.LittleEndian.Uint64(z[4:])
				if recordSize < 44 || recordSize > uint64(endAt-20)-pos-12 {
					return ErrUnsafe
				}
				records = binary.LittleEndian.Uint64(z[32:])
				length = binary.LittleEndian.Uint64(z[40:])
				offset = binary.LittleEndian.Uint64(z[48:])
				endAt = int64(pos)
			}
		}
	}
	if length > maxZIPDirectoryBytes || length > uint64(endAt) || offset > uint64(size) || records > length/46 {
		return ErrUnsafe
	}
	start := endAt - int64(length)
	// If the library switches to an earlier absolute directory, validate that
	// walk too. Checking a signature conservatively includes every such switch.
	if uint64(start) > offset {
		var sig [4]byte
		if _, err := r.ReadAt(sig[:], int64(offset)); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(sig[:]) == 0x02014b50 {
			if err := checkZIPDirectory(r, int64(offset), size, length, records); err != nil {
				return err
			}
		}
	}
	return checkZIPDirectory(r, start, size, length, records)
}

func checkZIPDirectory(r io.ReaderAt, start, size int64, declared, records uint64) error {
	var consumed, count uint64
	var header [46]byte
	for {
		at := start + int64(consumed)
		if at < 0 || at > size-4 {
			return ErrUnsafe
		}
		if _, err := r.ReadAt(header[:4], at); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(header[:4]) != 0x02014b50 {
			break
		}
		if _, err := r.ReadAt(header[4:], at+4); err != nil {
			return err
		}
		span := uint64(46) + uint64(binary.LittleEndian.Uint16(header[28:])) + uint64(binary.LittleEndian.Uint16(header[30:])) + uint64(binary.LittleEndian.Uint16(header[32:]))
		if span > maxZIPDirectoryBytes-consumed || consumed+span > declared || span > uint64(size-at) {
			return ErrUnsafe
		}
		consumed += span
		count++
	}
	if uint16(count) != uint16(records) {
		return ErrUnsafe
	}
	return nil
}
