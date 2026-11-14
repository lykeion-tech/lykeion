// Package compute defines the measurements reported by a person's daemon.
package compute

import "math"

type Sample struct {
	RSS uint64  `json:"rss"`
	CPU float64 `json:"cpu"`
}
type MachineSample struct {
	MemUsed   uint64  `json:"mem_used"`
	MemTotal  uint64  `json:"mem_total"`
	CPUUsed   float64 `json:"cpu_used"`
	Cores     int     `json:"cores"`
	DiskFree  uint64  `json:"disk_free"`
	DiskTotal uint64  `json:"disk_total"`
}

func (s Sample) Valid() bool { return s.CPU >= 0 && !math.IsNaN(s.CPU) && !math.IsInf(s.CPU, 0) }
func (s MachineSample) Valid() bool {
	return s.Cores > 0 && s.Cores <= 65536 && s.MemTotal > 0 && s.MemUsed <= s.MemTotal && s.DiskFree <= s.DiskTotal && s.CPUUsed >= 0 && s.CPUUsed <= float64(s.Cores)*100 && !math.IsNaN(s.CPUUsed) && !math.IsInf(s.CPUUsed, 0)
}
