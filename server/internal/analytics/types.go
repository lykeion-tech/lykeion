// Package analytics derives calendar usage from the persisted execution ledger.
package analytics

import (
	"time"

	"github.com/google/uuid"
)

type Run struct {
	ID, StudyID                      uuid.UUID
	AgentID                          *uuid.UUID
	AgentName, Role, Harness, Status string
	Attempt                          int32
	StartedAt                        *time.Time
}
type Event struct {
	RunID   uuid.UUID
	Attempt int32
	Type    string
	At      time.Time
}
type Usage struct {
	RunID                                uuid.UUID
	Attempt                              int32
	Input, Output, CacheRead, CacheWrite int64
	Cost                                 float64
	Reported                             bool
}
type Coverage struct {
	Tokens                string `json:"tokens"`
	Cost                  string `json:"cost"`
	Time                  string `json:"time"`
	Studies               string `json:"studies"`
	MissingUsageAttempts  int    `json:"missing_usage_attempts"`
	MissingTimingAttempts int    `json:"missing_timing_attempts"`
}
type Metrics struct {
	IncludesEstimates   bool     `json:"includes_estimates"`
	InputTokens         int64    `json:"input_tokens"`
	OrdinaryInputTokens int64    `json:"ordinary_input_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	CacheWriteTokens    int64    `json:"cache_write_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CostUSD             float64  `json:"cost_usd"`
	CostReportedUSD     float64  `json:"cost_reported_usd"`
	CostEstimatedUSD    float64  `json:"cost_estimated_usd"`
	RuntimeSeconds      float64  `json:"runtime_seconds"`
	Studies             int      `json:"studies"`
	Coverage            Coverage `json:"coverage"`
}
type Bucket struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Metrics
}
type Agent struct {
	AgentID   *uuid.UUID `json:"agent_id"`
	Name      string     `json:"name"`
	Roles     []string   `json:"roles"`
	Harnesses []string   `json:"harnesses"`
	Metrics
}
type Unassigned struct {
	Attempts int `json:"attempts"`
	Metrics
}
type Response struct {
	Timezone         string     `json:"timezone"`
	Scope            string     `json:"scope"`
	AssociationBasis string     `json:"association_basis"`
	Range            string     `json:"range"`
	Cadence          string     `json:"cadence"`
	Start            time.Time  `json:"start"`
	End              time.Time  `json:"end"`
	Now              time.Time  `json:"now"`
	Summary          Metrics    `json:"summary"`
	Buckets          []Bucket   `json:"buckets"`
	Agents           []Agent    `json:"agents"`
	Unassigned       Unassigned `json:"unassigned"`
}
