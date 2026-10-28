package daemon

import (
	"context"
	"os"
	"testing"
)

func TestProcessTreeSample(t *testing.T) {
	s, e := sampleProcessTree(context.Background(), os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	if s.RSSBytes <= 0 || s.Processes < 1 {
		t.Fatalf("%+v", s)
	}
}
func TestProcessTreeAggregation(t *testing.T) {
	s, e := parseProcessTree("10 1 10 100\n11 10 10 200\n12 11 10 300\n13 1 13 900\n", 10)
	if e != nil || s.RSSBytes != 600*1024 || s.Processes != 3 {
		t.Fatalf("%+v %v", s, e)
	}
}
