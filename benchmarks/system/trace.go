package system

import "github.com/EpicBlackWolfZ/microfat/benchmarks/schema"

type ExecEvent struct {
	ElapsedNS    int64  `json:"elapsed_ns"`
	Executable   string `json:"executable"`
	PeakRSSBytes *int64 `json:"cumulative_peak_rss_bytes"`
}

type ExecDiagnostic struct {
	Status        string                  `json:"status"`
	Reason        string                  `json:"reason,omitempty"`
	Events        []ExecEvent             `json:"exec_events"`
	Samples       []schema.ResourceSample `json:"samples"`
	Qualification string                  `json:"qualification"`
}
