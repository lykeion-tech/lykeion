package board

import "strings"

// DisplayPrefix is the prefix card numbers show with: the workspace's own,
// else the first four letters of its slug in capitals, else LYK when the slug
// has fewer than two letters.
func DisplayPrefix(prefix, slug string) string {
	if prefix != "" {
		return prefix
	}
	var b strings.Builder
	for _, c := range slug {
		if c >= 'a' && c <= 'z' && b.Len() < 4 {
			b.WriteRune(c - 'a' + 'A')
		}
	}
	if b.Len() < 2 {
		return "LYK"
	}
	return b.String()
}
