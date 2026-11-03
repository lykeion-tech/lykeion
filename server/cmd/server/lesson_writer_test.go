package main

import "testing"

func TestNoKeysNoWriter(t *testing.T) {
	for _, keys := range [][2]string{{"", ""}, {"haiku", " "}, {"", "jev"}} {
		if buildLessonWriter(nil, nil, keys[0], keys[1]) != nil {
			t.Fatal("built writer with missing key")
		}
	}
	if buildLessonWriter(nil, nil, "haiku", "jev") == nil {
		t.Fatal("expected configured writer")
	}
}
