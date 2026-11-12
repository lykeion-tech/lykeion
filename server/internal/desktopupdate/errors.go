package desktopupdate

import "errors"

// Categories are a closed protocol; underlying errors never cross the process boundary.
type Category string

const (
	Unsupported     Category = "unsupported"
	Unconfigured    Category = "unconfigured"
	Network         Category = "network"
	InvalidMetadata Category = "invalid_metadata"
	CacheFull       Category = "cache_full"
	CorruptState    Category = "corrupt_state"
	Generic         Category = "generic"
)

type checkError struct{ category Category }

func (e checkError) Error() string { return "update check unavailable" }

func ErrorCategory(err error) Category {
	var target checkError
	if errors.As(err, &target) {
		switch target.category {
		case Unsupported, Unconfigured, Network, InvalidMetadata, CacheFull, CorruptState:
			return target.category
		}
	}
	return Generic
}
