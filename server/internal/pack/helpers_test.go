package pack

import (
	"io/fs"

	"lykeion/server/internal/pack/check"
)

type checkRecord = check.Record

const modeSymlink = uint32(fs.ModeSymlink)

func modeOf(m uint32) fs.FileMode { return fs.FileMode(m) | 0o777 }
