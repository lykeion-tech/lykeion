package labmemory

import "net/url"

// RefPath mirrors sourcePath in apps/dashboard/src/features/aristotle-window/api.ts:
// the dashboard route (hash-router path) a citation links to, or "".
func RefPath(r DisplayRef) string {
	ref, err := ParseRef(r.Ref)
	if err != nil {
		return ""
	}
	switch ref.Kind {
	case "workspace":
		return "/settings?tab=workspace"
	case "proposal":
		return "/studies"
	case "decision":
		return "/decisions/" + url.PathEscape(ref.ID.String())
	}
	if r.StudyID == nil {
		return ""
	}
	study := url.PathEscape(r.StudyID.String())
	switch ref.Kind {
	case "study":
		return "/studies/" + study
	case "report":
		return "/decisions/report/" + study
	case "record", "comment":
		return "/studies/" + study + "?cite=" + url.QueryEscape(r.Ref)
	}
	return ""
}
