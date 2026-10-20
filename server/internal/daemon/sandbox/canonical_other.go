//go:build !darwin

package sandbox

// canonical returns p, already resolved by the walk; bwrap binds by path, and
// the file system answers to that spelling.
func canonical(p string) (string, error) { return p, nil }
