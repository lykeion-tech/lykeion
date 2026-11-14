package analytics

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type attemptKey struct {
	run     uuid.UUID
	attempt int32
}
type attempt struct {
	start, end time.Time
	dispatched bool
	usage      []Usage
}
type accumulator struct {
	m                     Metrics
	studies               map[uuid.UUID]bool
	knownUsage, knownTime int
	reported, estimated   big.Rat
}

func newAccumulator() *accumulator { return &accumulator{studies: map[uuid.UUID]bool{}} }
func (a *accumulator) addUsage(u Usage) {
	a.m.OrdinaryInputTokens += u.Input
	a.m.CacheReadTokens += u.CacheRead
	a.m.CacheWriteTokens += u.CacheWrite
	a.m.InputTokens += u.Input + u.CacheRead + u.CacheWrite
	a.m.OutputTokens += u.Output
	value := new(big.Rat).SetFloat64(u.Cost)
	if u.Reported {
		a.reported.Add(&a.reported, value)
	} else {
		a.estimated.Add(&a.estimated, value)
		a.m.IncludesEstimates = true
	}
	a.knownUsage++
}
func coverage(known, missing int) string {
	if missing == 0 {
		return "complete"
	}
	if known == 0 {
		return "missing"
	}
	return "partial"
}
func (a *accumulator) result() Metrics {
	a.m.Studies = len(a.studies)
	a.m.CostReportedUSD, _ = a.reported.Float64()
	a.m.CostEstimatedUSD, _ = a.estimated.Float64()
	sum := new(big.Rat).Add(&a.reported, &a.estimated)
	a.m.CostUSD, _ = sum.Float64()
	a.m.Coverage.Tokens = coverage(a.knownUsage, a.m.Coverage.MissingUsageAttempts)
	a.m.Coverage.Cost = a.m.Coverage.Tokens
	a.m.Coverage.Time = coverage(a.knownTime, a.m.Coverage.MissingTimingAttempts)
	a.m.Coverage.Studies = coverage(len(a.studies), a.m.Coverage.MissingTimingAttempts)
	return a.m
}
func Bounds(now time.Time, timezone, span, cadence string) (time.Time, time.Time, *time.Location, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" || timezone == "Local" {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("invalid timezone")
	}
	days, err := strconv.Atoi(strings.TrimSuffix(span, "d"))
	if err != nil || span != fmt.Sprintf("%dd", days) || days != 1 && days != 7 && days != 30 && days != 90 {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("invalid range")
	}
	if cadence != "daily" && cadence != "weekly" {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("invalid cadence")
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return today.AddDate(0, 0, 1-days).UTC(), today.AddDate(0, 0, 1).UTC(), loc, nil
}
func Aggregate(now time.Time, timezone, span, cadence, scope string, runs []Run, events []Event, usage []Usage) (Response, error) {
	start, end, loc, err := Bounds(now, timezone, span, cadence)
	if err != nil {
		return Response{}, err
	}
	out := Response{Timezone: timezone, Scope: scope, AssociationBasis: "current", Range: span, Cadence: cadence, Start: start, End: end, Now: now.UTC(), Buckets: []Bucket{}, Agents: []Agent{}}
	for cursor := start; cursor.Before(end); {
		next := cursor.In(loc).AddDate(0, 0, 1)
		if cadence == "weekly" {
			days := (8 - int(cursor.In(loc).Weekday())) % 7
			if days == 0 {
				days = 7
			}
			next = cursor.In(loc).AddDate(0, 0, days)
		}
		if next.After(end) {
			next = end
		}
		out.Buckets = append(out.Buckets, Bucket{Start: cursor.UTC(), End: next.UTC()})
		cursor = next
	}
	all := newAccumulator()
	unassigned := newAccumulator()
	buckets := make([]*accumulator, len(out.Buckets))
	for i := range buckets {
		buckets[i] = newAccumulator()
	}
	attempts := map[attemptKey]*attempt{}
	get := func(k attemptKey) *attempt {
		a := attempts[k]
		if a == nil {
			a = &attempt{}
			attempts[k] = a
		}
		return a
	}
	validRuns := map[uuid.UUID]Run{}
	for _, r := range runs {
		if r.StudyID != uuid.Nil {
			validRuns[r.ID] = r
			for n := int32(1); n <= r.Attempt; n++ {
				get(attemptKey{r.ID, n})
			}
		}
	}
	for _, e := range events {
		if _, ok := validRuns[e.RunID]; !ok || e.Attempt <= 0 {
			continue
		}
		a := get(attemptKey{e.RunID, e.Attempt})
		switch e.Type {
		case "agent_run.dispatched":
			a.dispatched = true
		case "agent_run.started":
			if a.start.IsZero() || e.At.Before(a.start) {
				a.start = e.At
			}
		case "agent_run.completed", "agent_run.failed", "agent_run.cancelled", "agent_run.queued", "agent_run.requeued":
			if a.end.IsZero() || e.At.Before(a.end) {
				a.end = e.At
			}
		}
	}
	for _, u := range usage {
		if _, ok := validRuns[u.RunID]; ok && u.Attempt > 0 {
			get(attemptKey{u.RunID, u.Attempt}).usage = append(get(attemptKey{u.RunID, u.Attempt}).usage, u)
		}
	}
	agentAcc := map[string]*accumulator{}
	agentInfo := map[string]*Agent{}
	for k, a := range attempts {
		r := validRuns[k.run]
		stop := a.end
		if stop.IsZero() && k.attempt == r.Attempt && r.Status == "running" && !a.start.IsZero() {
			stop = now
		}
		interval := !a.start.IsZero() && !stop.IsZero() && !stop.Before(a.start)
		hasUsage := len(a.usage) > 0
		// Prior retries and run-level start evidence require coverage even if a start event is absent.
		executable := !a.start.IsZero() || hasUsage || r.StartedAt != nil || k.attempt < r.Attempt || !a.dispatched && (r.Status == "completed" || r.Status == "failed")
		if !executable {
			continue
		}
		if hasUsage && a.end.IsZero() {
			out.Unassigned.Attempts++
			for _, u := range a.usage {
				unassigned.addUsage(u)
			}
		}
		// Without a durable date, usage belongs only to the unassigned disclosure.
		if a.start.IsZero() && a.end.IsZero() {
			continue
		}
		dated := !a.end.IsZero() && !a.end.Before(start) && a.end.Before(end)
		overlaps := interval && a.start.Before(end) && stop.After(start)
		unknownTime := !interval && (dated || a.end.IsZero())
		if !dated && !overlaps && !unknownTime {
			continue
		}
		key := "unattributed"
		if r.AgentID != nil {
			key = r.AgentID.String()
		}
		if agentAcc[key] == nil {
			agentAcc[key] = newAccumulator()
			name := r.AgentName
			if r.AgentID == nil {
				name = "Unattributed"
			}
			agentInfo[key] = &Agent{AgentID: r.AgentID, Name: name, Roles: []string{}, Harnesses: []string{}}
		}
		info := agentInfo[key]
		info.Roles = appendUnique(info.Roles, r.Role)
		info.Harnesses = appendUnique(info.Harnesses, r.Harness)
		apply := func(acc *accumulator, lo, hi time.Time) {
			usageHere := !a.end.IsZero() && !a.end.Before(lo) && a.end.Before(hi)
			overlap := interval && a.start.Before(hi) && stop.After(lo)
			unknown := !interval && (usageHere || a.end.IsZero())
			if overlap {
				left, right := a.start, stop
				if left.Before(lo) {
					left = lo
				}
				if right.After(hi) {
					right = hi
				}
				acc.m.RuntimeSeconds += right.Sub(left).Seconds()
				acc.knownTime++
				acc.studies[r.StudyID] = true
			}
			if usageHere && hasUsage {
				for _, u := range a.usage {
					acc.addUsage(u)
				}
				acc.studies[r.StudyID] = true
			}
			if (!hasUsage || a.end.IsZero()) && (overlap || usageHere || unknown) {
				acc.m.Coverage.MissingUsageAttempts++
			}
			if unknown {
				acc.m.Coverage.MissingTimingAttempts++
			}
		}
		apply(all, start, end)
		apply(agentAcc[key], start, end)
		for i, b := range out.Buckets {
			apply(buckets[i], b.Start, b.End)
		}
	}
	out.Summary = all.result()
	out.Unassigned.Metrics = unassigned.result()
	for i := range out.Buckets {
		out.Buckets[i].Metrics = buckets[i].result()
	}
	keys := make([]string, 0, len(agentAcc))
	for key := range agentAcc {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		row := agentInfo[key]
		sort.Strings(row.Roles)
		sort.Strings(row.Harnesses)
		row.Metrics = agentAcc[key].result()
		out.Agents = append(out.Agents, *row)
	}
	return out, nil
}
func appendUnique(items []string, value string) []string {
	if value == "" {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
