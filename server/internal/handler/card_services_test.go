package handler

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestHiddenServiceClientsRequireDistinctBasenames(t *testing.T) {
	for _, pair := range [][2]string{{"one/client", "two/client"}, {"one/CLIENT", "two/client"}, {"one/café", "two/cafe\u0301"}} {
		services := []newService{{Name: "first", AttachmentID: uuid.New(), Start: "run", Client: pair[0]}, {Name: "second", AttachmentID: uuid.New(), Start: "run", Client: pair[1]}}
		err := checkServices(services)
		if err == nil || !strings.Contains(err.Error(), "duplicate hidden service client filename:") {
			t.Fatalf("%v: %v", pair, err)
		}
		services[1].Client = "two/different"
		if err := checkServices(services); err != nil {
			t.Fatal(err)
		}
	}
}
