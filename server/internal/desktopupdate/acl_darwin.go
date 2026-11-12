//go:build darwin

package desktopupdate

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Reject grants and malformed ACLs; deny-only ACLs cannot broaden access.
func privateACL(fd int) bool {
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buffer [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, 0)
	if errno != 0 {
		return false
	}
	return privateACLBytes(buffer[:])
}

func privateACLBytes(buffer []byte) bool {
	if len(buffer) < 12 {
		return false
	}
	order := binary.LittleEndian
	total := order.Uint32(buffer[0:4])
	offset := int32(order.Uint32(buffer[4:8]))
	size := order.Uint32(buffer[8:12])
	if total < 12 || total > uint32(len(buffer)) || offset != 8 {
		return false
	}
	start := uint64(4) + uint64(offset)
	if size == 0 {
		return total == 12
	}
	if start > uint64(total) || uint64(size) != uint64(total)-start || size < 44 {
		return false
	}
	data := buffer[start : start+uint64(size)]
	if order.Uint32(data[0:4]) != 0x012cc16d {
		return false
	}
	count := order.Uint32(data[36:40])
	if order.Uint32(data[40:44]) != 0 {
		return false
	}
	if count == ^uint32(0) {
		return size == 44
	}
	if count > 128 || uint64(size) != 44+uint64(count)*24 {
		return false
	}
	for i := uint32(0); i < count; i++ {
		flags := order.Uint32(data[44+i*24+16 : 44+i*24+20])
		if flags != 2 {
			return false
		}
	}
	return true
}
