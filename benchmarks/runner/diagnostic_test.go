package runner

import (
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/stretchr/testify/assert"
)

const diagnosticPartial = "partial"

func TestMissingPayloadExecDoesNotAttributePhases(t *testing.T) {
	t.Parallel()
	const observed, memfd = "observed", "memfd"
	for _, tt := range []struct {
		name, mode, status, want string
		entries                  int
	}{
		{"native payload captured", nativeMode, observed, observed, 2},
		{"native helper only", nativeMode, observed, diagnosticPartial, 1},
		{"memfd payload captured", memfd, observed, observed, 3},
		{"memfd launcher only", memfd, observed, diagnosticPartial, 2},
		{"cache launcher only", "cache", observed, diagnosticPartial, 2},
		{"trace unavailable", memfd, "unavailable", "unavailable", 0},
		{"failed child", memfd, "failed", "failed", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diagnostic := system.ExecDiagnostic{Status: tt.status, Events: make([]system.ExecEvent, tt.entries),
				Samples: []schema.ResourceSample{{Phase: "exec-stage-2", Metrics: map[string]schema.Measurement{
					"rss_bytes": schema.Measured(4096, "bytes", "exec-stage-2", "proc/status"),
				}}}}
			qualifyExecDiagnostic(&diagnostic, tt.mode)
			assert.Equal(t, tt.want, diagnostic.Status)
			assert.Len(t, diagnostic.Events, tt.entries)
			if tt.want == "partial" {
				assert.Contains(t, diagnostic.Reason, "payload exec entry was not captured")
				assert.Equal(t, "unknown-exec-stage", diagnostic.Samples[0].Phase)
				assert.Equal(t, "unknown-exec-stage", diagnostic.Samples[0].Metrics["rss_bytes"].Phase)
			} else {
				assert.Equal(t, "exec-stage-2", diagnostic.Samples[0].Phase)
			}
		})
	}
}
