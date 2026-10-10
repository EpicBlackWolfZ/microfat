package runtimequalify

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

func cacheFirstDispatchLine(t *testing.T, e Evidence) string {
	t.Helper()
	return "[microfat] " + jsonString(t, format.DispatchTelemetry{Event: format.EventDispatch,
		SelectedVariant: e.Lineage.SelectedTier, SelectedSHA256: e.Lineage.PayloadSHA256,
		SelectedSizeBytes: e.Lineage.PayloadSize, ExecMode: Cache}) + "\n"
}

func cacheFirstCreateDenialLine(t *testing.T) string {
	t.Helper()
	return "[microfat] " + jsonString(t, format.ErrorTelemetry{Event: format.EventError,
		Stage: format.StageMemfdCreate, Error: "policy denied memfd creation", ErrnoName: "EPERM"}) + "\n"
}

func TestCacheFirstQualificationTelemetry(t *testing.T) {
	t.Parallel()
	t.Run("read-only warm hit bypasses memfd policy", func(t *testing.T) {
		t.Parallel()
		e := validEvidence(t, caseFor(t, readOnlyWarm, Auto))
		e.Result.Executions[0].Stderr = cacheFirstDispatchLine(t, e)
		require.NoError(t, ValidateEvidence(e))
		e.Result.Executions[0].Stderr = cacheFirstCreateDenialLine(t) + e.Result.Executions[0].Stderr
		require.ErrorContains(t, ValidateEvidence(e), "unexpected telemetry sequence")
	})
	t.Run("concurrent first lookups allow hits and misses; warm phase requires hits", func(t *testing.T) {
		t.Parallel()
		e := validEvidence(t, caseFor(t, "concurrent-0", Auto))
		for i := range e.Result.Executions {
			run := &e.Result.Executions[i]
			if run.Phase != 0 || i%2 == 0 {
				run.Stderr = cacheFirstDispatchLine(t, e)
			}
		}
		require.NoError(t, ValidateEvidence(e), "a peer can complete materialization before a first lookup")
		for i := range e.Result.Executions {
			if e.Result.Executions[i].Phase == 1 {
				e.Result.Executions[i].Stderr = cacheFirstCreateDenialLine(t) + e.Result.Executions[i].Stderr
				break
			}
		}
		require.ErrorContains(t, ValidateEvidence(e), "unexpected telemetry sequence")
	})
	for _, scenario := range []string{symlink, fifo, insecure} {
		t.Run(scenario+" rejects before memfd policy", func(t *testing.T) {
			t.Parallel()
			e := validEvidence(t, caseFor(t, scenario, Auto))
			require.NoError(t, ValidateEvidence(e))
			require.NotContains(t, e.Result.Executions[0].Stderr, format.StageMemfdCreate)
			e.Result.Executions[0].Stderr = cacheFirstCreateDenialLine(t) + e.Result.Executions[0].Stderr
			require.ErrorContains(t, ValidateEvidence(e), "unexpected telemetry sequence")
		})
	}
}

func TestSourceCorruptionQualificationStartsWithoutWarmCache(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{corruptPayload, corruptDictionary} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			c, _ := fakeController(t, runtime.GOARCH)
			require.NoError(t, c.acquire())
			require.NoError(t, c.buildHelpers())
			item := caseFor(t, scenario, Auto)
			lineage, err := c.pack(item.Configuration)
			require.NoError(t, err)
			original, err := os.ReadFile(lineage.Bundle)
			require.NoError(t, err)
			req, err := c.request(item, lineage)
			require.NoError(t, err)
			entries, err := os.ReadDir(filepath.Join(req.Root, "cache"))
			require.NoError(t, err)
			require.Empty(t, entries, "source-corruption proof must reach extraction instead of a verified warm hit")
			corrupt, err := os.ReadFile(filepath.Join(req.Root, "source", "app"))
			require.NoError(t, err)
			require.NotEqual(t, original, corrupt)
			retained, err := os.ReadFile(lineage.Bundle)
			require.NoError(t, err)
			require.Equal(t, original, retained, "corruption must only affect the private fixture copy")
			for _, event := range expectedSequence(item, 0) {
				require.False(t, strings.Contains(event, "cache"), "corrupt source cannot reach cache fallback")
			}
			e := validEvidence(t, item)
			require.NoError(t, ValidateEvidence(e))
			e.Result.CacheSnapshots[0].Entries = []mountfixture.CacheEntry{{Name: e.Lineage.PayloadSHA256}}
			require.ErrorContains(t, ValidateEvidence(e), "source corruption left a cache artifact")
		})
	}
}
