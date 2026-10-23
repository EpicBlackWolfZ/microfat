//go:build linux

package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	prewarmAllFlag          = "--all"
	prewarmLevelFlag        = "--level"
	prewarmCacheDirFlag     = "--cache-dir"
	prewarmInsecureDirState = "insecure-directory"
	prewarmFIFOState        = "fifo"
	prewarmPlanFlag         = "--plan"
	prewarmPayloadCapFlag   = "--max-payload-bytes"
	prewarmPlanMissing      = "missing"
	prewarmPlanValid        = "valid"
	prewarmPlanCorruptSafe  = "corrupt_safe"
	prewarmPlanUnsafe       = "unsafe"
)

type prewarmPlanEntry struct {
	Level                   string `json:"level"`
	SHA256                  string `json:"sha256"`
	UncompressedSize        uint64 `json:"uncompressed_size"`
	CachePath               string `json:"cache_path"`
	CacheStatus             string `json:"cache_status"`
	CacheError              string `json:"cache_error"`
	DecoderAvailable        bool   `json:"decoder_available"`
	DecoderError            string `json:"decoder_error"`
	MaterializationPossible bool   `json:"materialization_possible"`
	CanPrewarm              bool   `json:"can_prewarm"`
}

type prewarmPlanOutput struct {
	Event                     string             `json:"event"`
	AppName                   string             `json:"app_name"`
	TargetOS                  string             `json:"target_os"`
	TargetArch                string             `json:"target_arch"`
	Entries                   []prewarmPlanEntry `json:"entries"`
	UniquePayloadCount        int                `json:"unique_payload_count"`
	UniquePayloadBytes        uint64             `json:"unique_payload_bytes"`
	VerifiedCacheBytes        uint64             `json:"verified_cache_bytes"`
	NeedsMaterializationBytes uint64             `json:"needs_materialization_bytes"`
	BlockedPayloadBytes       uint64             `json:"blocked_payload_bytes"`
}

func readPrewarmPlan(t *testing.T, fat, dir string, args ...string) (prewarmPlanOutput, string, error) {
	t.Helper()
	flags := []string{cacheExecutablePrewarm, fat, prewarmPlanFlag, cacheExecutableJSON, prewarmCacheDirFlag, dir}
	stdout, stderr, err := runCacheFirstProcess(t, cliPath, cacheFirstEnv(dir), append(flags, args...)...)
	var plan prewarmPlanOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &plan), "plan JSON: %s\n%s", stdout, stderr)
	require.Equal(t, "prewarm_plan", plan.Event)
	return plan, stderr, err
}

func preparePrewarmPlanState(t *testing.T, state, dir string) {
	t.Helper()
	if state == prewarmPlanMissing {
		return
	}
	if state == prewarmInsecureDirState {
		const insecureMode = 0o777
		require.NoError(t, os.Mkdir(dir, privateDirPerm))
		require.NoError(t, os.Chmod(dir, insecureMode))
		return
	}
	output, err := runCacheCheck(t, cacheFirstEnv(dir), cliPath,
		cacheExecutablePrewarm, goldenFatBin, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir)
	require.NoError(t, err, string(output))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	path := filepath.Join(dir, entries[0].Name())
	switch state {
	case prewarmPlanCorruptSafe:
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.NotEmpty(t, data)
		data[0] ^= 0xff
		require.NoError(t, os.WriteFile(path, data, defaultFilePerm))
	case cacheFirstSymlink, prewarmFIFOState:
		prepareUnsafeCacheFirstEntry(t, path, state)
	}
}

func TestPrewarmPlanPreservesFilesystem(t *testing.T) {
	t.Parallel()
	states := []struct {
		name   string
		status string
		fails  bool
	}{
		{name: prewarmPlanMissing, status: prewarmPlanMissing},
		{name: prewarmPlanValid, status: prewarmPlanValid},
		{name: prewarmPlanCorruptSafe, status: prewarmPlanCorruptSafe},
		{name: prewarmInsecureDirState, status: prewarmPlanUnsafe, fails: true},
		{name: cacheFirstSymlink, status: prewarmPlanUnsafe, fails: true},
		{name: prewarmFIFOState, status: prewarmPlanUnsafe, fails: true},
	}
	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			preparePrewarmPlanState(t, state.name, dir)
			before := cacheTreeSnapshot(t, root)
			plan, stderr, err := readPrewarmPlan(t, goldenFatBin, dir, prewarmLevelFlag, updateTier())
			if state.fails {
				require.Error(t, err, stderr)
			} else {
				require.NoError(t, err, stderr)
			}
			require.Equal(t, before, cacheTreeSnapshot(t, root), "planning changed the cache filesystem")
			require.Len(t, plan.Entries, 1)
			entry := plan.Entries[0]
			require.Equal(t, updateTier(), entry.Level)
			require.Equal(t, state.status, entry.CacheStatus)
			require.True(t, entry.DecoderAvailable)
			require.Equal(t, entry.UncompressedSize, plan.UniquePayloadBytes)
			require.Equal(t, plan.UniquePayloadBytes,
				plan.VerifiedCacheBytes+plan.NeedsMaterializationBytes+plan.BlockedPayloadBytes)
			switch {
			case state.status == prewarmPlanValid:
				require.Equal(t, plan.UniquePayloadBytes, plan.VerifiedCacheBytes)
			case state.fails:
				require.Equal(t, plan.UniquePayloadBytes, plan.BlockedPayloadBytes)
				require.NotEmpty(t, entry.CacheError)
				require.False(t, entry.MaterializationPossible)
			default:
				require.Equal(t, plan.UniquePayloadBytes, plan.NeedsMaterializationBytes)
				require.True(t, entry.MaterializationPossible)
			}
		})
	}
}

func TestPrewarmPayloadAdmissionPrecedesCacheMutation(t *testing.T) {
	t.Parallel()
	for _, state := range []string{prewarmPlanMissing, prewarmPlanValid, prewarmPlanCorruptSafe,
		prewarmInsecureDirState, cacheFirstSymlink, prewarmFIFOState} {
		for _, mode := range []string{"apply", "plan", inputVerifyCommand} {
			t.Run(state+"/"+mode, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				dir := filepath.Join(root, "cache")
				preparePrewarmPlanState(t, state, dir)
				before := cacheTreeSnapshot(t, root)
				args := []string{cacheExecutablePrewarm, goldenFatBin, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir,
					prewarmPayloadCapFlag + "=0"}
				if mode != "apply" {
					args = append(args, "--"+mode)
				}
				_, stderr, err := runCacheFirstProcess(t, cliPath, cacheFirstEnv(dir), args...)
				require.Error(t, err)
				require.Contains(t, stderr, "exceed")
				require.Contains(t, stderr, "payload")
				require.Equal(t, before, cacheTreeSnapshot(t, root), "rejected admission changed the cache")
			})
		}
	}
}

func TestPrewarmPayloadAdmissionBoundaries(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "cache")
	plan, stderr, err := readPrewarmPlan(t, goldenFatBin, dir, prewarmLevelFlag, updateTier())
	require.NoError(t, err, stderr)
	require.Positive(t, plan.UniquePayloadBytes)
	limit := strconv.FormatUint(plan.UniquePayloadBytes, 10)
	for _, mode := range []string{"apply", "plan", inputVerifyCommand} {
		args := []string{cacheExecutablePrewarm, goldenFatBin, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir,
			prewarmPayloadCapFlag + "=" + limit}
		if mode != "apply" {
			args = append(args, "--"+mode)
		}
		output, runErr := runCacheCheck(t, cacheFirstEnv(dir), cliPath, args...)
		require.NoError(t, runErr, "mode %s: %s", mode, output)
	}
	before := cacheTreeSnapshot(t, dir)
	_, stderr, err = runCacheFirstProcess(t, cliPath, cacheFirstEnv(dir),
		cacheExecutablePrewarm, goldenFatBin, prewarmPlanFlag, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir,
		prewarmPayloadCapFlag+"="+strconv.FormatUint(plan.UniquePayloadBytes-1, 10))
	require.Error(t, err, stderr)
	require.Contains(t, stderr, "exceed")
	require.Equal(t, before, cacheTreeSnapshot(t, dir))
}

func TestPrewarmPayloadCapInputAndFlagCombinations(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "-1", "+1", "1.0", "0x10", " 1", "1 ", "18446744073709551616"} {
		t.Run("invalid cap/"+strconv.Quote(value), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			before := cacheTreeSnapshot(t, root)
			_, stderr, err := runCacheFirstProcess(t, cliPath, cacheFirstEnv(dir),
				cacheExecutablePrewarm, goldenFatBin, prewarmCacheDirFlag, dir, prewarmPayloadCapFlag+"="+value)
			require.Error(t, err)
			require.Contains(t, stderr, "max-payload-bytes")
			require.Equal(t, before, cacheTreeSnapshot(t, root))
		})
	}
	for _, args := range [][]string{
		{prewarmPlanFlag, "--verify"},
		{prewarmAllFlag, prewarmLevelFlag, updateTier()},
		{prewarmAllFlag, "--level="},
		{prewarmPlanFlag, prewarmLevelFlag, "v99"},
	} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			before := cacheTreeSnapshot(t, root)
			command := append([]string{cacheExecutablePrewarm, goldenFatBin, prewarmCacheDirFlag, dir}, args...)
			_, stderr, err := runCacheFirstProcess(t, cliPath, cacheFirstEnv(dir), command...)
			require.Error(t, err, stderr)
			require.Equal(t, before, cacheTreeSnapshot(t, root))
		})
	}
	t.Run("maximum uint64 accepted", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "cache")
		_, stderr, err := readPrewarmPlan(t, goldenFatBin, dir, prewarmPayloadCapFlag+"=18446744073709551615")
		require.NoError(t, err, stderr)
		_, statErr := os.Stat(dir)
		require.ErrorIs(t, statErr, os.ErrNotExist)
	})
}

func TestPrewarmPlanTextAndExistingJSON(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "cache")
	stdout, stderr, err := runCacheFirstProcess(t, cliPath, cacheFirstEnv(dir),
		cacheExecutablePrewarm, goldenFatBin, prewarmPlanFlag, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir)
	require.NoError(t, err, stderr)
	require.Contains(t, stdout, "Unique selected payload bytes:")
	require.Contains(t, stdout, updateTier())
	require.Contains(t, stdout, prewarmPlanMissing)
	_, statErr := os.Stat(dir)
	require.ErrorIs(t, statErr, os.ErrNotExist)
	for _, verify := range []bool{false, true} {
		args := []string{cacheExecutablePrewarm, goldenFatBin, cacheExecutableJSON, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir}
		if verify {
			args = append(args, "--verify")
		}
		stdout, stderr, err = runCacheFirstProcess(t, cliPath, cacheFirstEnv(dir), args...)
		require.NoError(t, err, stderr)
		var output map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(stdout), &output))
		require.Len(t, output, 5, "existing prewarm telemetry gained fields without --plan: %s", stdout)
		for _, field := range []string{"event", "timestamp_unix_nano", "app_name", "cache_dir", "results"} {
			require.Contains(t, output, field)
		}
		var event string
		require.NoError(t, json.Unmarshal(output["event"], &event))
		require.Equal(t, cacheExecutablePrewarm, event)
	}
}
