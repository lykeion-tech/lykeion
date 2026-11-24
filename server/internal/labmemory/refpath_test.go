package labmemory

import (
	"testing"

	"github.com/google/uuid"
)

// The fixtures mirror sourcePath in apps/dashboard/src/features/aristotle-window/api.ts.
func TestRefPathMatchesDashboard(t *testing.T) {
	id, study := uuid.New(), uuid.New()
	for _, c := range []struct {
		r    DisplayRef
		want string
	}{
		{DisplayRef{Ref: "workspace:context"}, "/settings?tab=workspace"},
		{DisplayRef{Ref: "proposal:" + id.String()}, "/studies"},
		{DisplayRef{Ref: "decision:" + id.String()}, "/decisions/" + id.String()},
		{DisplayRef{Ref: "study:" + id.String(), StudyID: &study}, "/studies/" + study.String()},
		{DisplayRef{Ref: "report:" + study.String() + "#concept:x", StudyID: &study}, "/decisions/report/" + study.String()},
		{DisplayRef{Ref: "record:" + id.String(), StudyID: &study}, "/studies/" + study.String() + "?cite=record%3A" + id.String()},
		{DisplayRef{Ref: "comment:" + id.String(), StudyID: &study}, "/studies/" + study.String() + "?cite=comment%3A" + id.String()},
		{DisplayRef{Ref: "record:" + id.String()}, ""},
		{DisplayRef{Ref: "lesson:" + id.String(), StudyID: &study}, ""},
	} {
		if got := RefPath(c.r); got != c.want {
			t.Errorf("%s: %q, want %q", c.r.Ref, got, c.want)
		}
	}
}
