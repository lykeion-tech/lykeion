package service

import "regexp"

// continuityScope is the shape of a run's continuity scope, which only the
// server builds: kind:<uuid> then name:value segments, e.g.
// card:<id>/column:validation/round:2/role:leader.
var continuityScope = regexp.MustCompile(`^(?:[a-z]+|lab-chat):[0-9a-f-]{36}(/[a-z]+:[A-Za-z0-9_.-]{1,64})*$`)

// ValidContinuityScope says whether scope has the shape a daemon accepts.
func ValidContinuityScope(scope string) bool { return continuityScope.MatchString(scope) }
