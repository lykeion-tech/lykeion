package coresearch

import "testing"

func TestRerunCopiesAndCompares(t *testing.T) {
	if !OutputsMatch(map[string]string{"results/a": "one"}, map[string]string{"results/a": "one"}) {
		t.Fatal("matching outputs")
	}
	if OutputsMatch(map[string]string{"results/a": "one"}, map[string]string{"results/a": "two"}) || OutputsMatch(map[string]string{"results/a": "one"}, map[string]string{}) {
		t.Fatal("changed outputs")
	}
}
