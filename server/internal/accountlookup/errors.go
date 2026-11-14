package accountlookup

import "errors"

// ErrUnavailable reports that the effective account could not be resolved.
var ErrUnavailable = errors.New("cannot resolve account through system NSS")
