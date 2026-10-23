//go:build linux

package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	creationEpochVariable        = "SOURCE_DATE_EPOCH"
	creationEpochValue           = "-123456789"
	creationEpochSeconds         = int64(-123456789)
	creationEpochApp             = "reproducible-app"
	creationEpochPayload         = "manifest payload"
	creationEpochZstd            = "zstd:fastest"
	creationEpochNone            = "none"
	creationEpochLZ4             = "lz4"
	creationEpochNameFlag        = "--name"
	creationEpochCompressionFlag = "--compression"
)

type creationEpochIndex struct {
	CreatedUnix    int64            `json:"created_unix"`
	DictionarySize int64            `json:"dictionary_size"`
	Variants       []inspectVariant `json:"variants"`
}

func creationEpochLevels() []string {
	levels := make([]string, 0, len(goldenVariantBins))
	for level := range goldenVariantBins {
		levels = append(levels, level)
	}
	sort.Strings(levels)
	return levels
}

func creationEpochCommand(t *testing.T, epoch *string, args ...string) {
	t.Helper()
	cmd := exec.Command(cliPath, args...)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != creationEpochVariable {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
	if epoch != nil {
		cmd.Env = append(cmd.Env, creationEpochVariable+"="+*epoch)
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "microfat %s: %s", strings.Join(args, " "), out)
}

func inspectCreationEpoch(t *testing.T, path string) creationEpochIndex {
	t.Helper()
	// Output keeps Format v1's deprecation warning on stderr, outside JSON.
	out, err := exec.Command(cliPath, "inspect", "--json", path).Output()
	require.NoError(t, err)
	var index creationEpochIndex
	require.NoError(t, json.Unmarshal(out, &index), "inspect JSON: %s", out)
	return index
}

func requireRepeatedCreationEpoch(t *testing.T, output string, invoke func(reverse bool)) creationEpochIndex {
	t.Helper()
	invoke(false)
	first, err := os.ReadFile(output)
	require.NoError(t, err)
	invoke(true)
	second, err := os.ReadFile(output)
	require.NoError(t, err)
	require.True(t, bytes.Equal(first, second), "repeated packaging changed complete artifact bytes")
	require.Equal(t, sha256.Sum256(first), sha256.Sum256(second), "complete artifact SHA-256 differs")
	index := inspectCreationEpoch(t, output)
	require.Equal(t, creationEpochSeconds, index.CreatedUnix)
	verifyFatIntegrity(t, output)
	return index
}

func requireEpochArtifactExecution(t *testing.T, output, level, expected string) {
	t.Helper()
	for _, mode := range []string{execModeMemfd, execModeCache} {
		env := stubInfoProcessEnvironment(filepath.Join(t.TempDir(), "cache"),
			"MICROFAT_FORCE_LEVEL="+level, "MICROFAT_EXEC_MODE="+mode, envDebugTrue)
		stdout, stderr, exitCode, err := executeFatBinary(t, output, env)
		require.NoError(t, err, "mode %s exited %d: %s", mode, exitCode, stderr)
		require.Contains(t, stdout, expected)
		require.Contains(t, stderr, "exec_mode="+mode)
	}
}

func TestReproducibleEpochDirectCompatibility(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	minimalStub := buildMinimalLauncherStub(t, dir)
	levels := creationEpochLevels()
	const pairSize = 2
	require.GreaterOrEqual(t, len(levels), pairSize)
	levels = levels[:pairSize]
	stubs := []struct{ name, path string }{{launcherFullProfile, stubPath}, {launcherMinimalProfile, minimalStub}}
	for _, version := range []int{1, 2} {
		for _, stub := range stubs {
			for _, compression := range []string{creationEpochNone, creationEpochLZ4, creationEpochZstd} {
				t.Run(fmt.Sprintf("format%d/%s/%s", version, stub.name, compression), func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "app.fat")
					index := requireRepeatedCreationEpoch(t, output, func(reverse bool) {
						args := []string{inputPackCommand, mountArchFlag, currentHostArch, inputStubFlag, stub.path,
							creationEpochNameFlag, creationEpochApp, "--format-version", strconv.Itoa(version),
							creationEpochCompressionFlag, compression, "-o", output}
						ordered := slices.Clone(levels)
						if reverse {
							slices.Reverse(ordered)
						}
						for _, level := range ordered {
							args = append(args, "-v", level+"="+goldenVariantBins[level])
						}
						epoch := creationEpochValue
						creationEpochCommand(t, &epoch, args...)
					})
					require.Len(t, index.Variants, pairSize)
					require.Zero(t, index.DictionarySize)
					requireEpochArtifactExecution(t, output, levels[0], "golden:variant="+levels[0])
				})
			}
		}
	}
}

func TestReproducibleEpochManifestCompatibility(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	minimalStub := buildMinimalLauncherStub(t, dir)
	pkgDir := filepath.Join(dir, "source")
	writeManifestPackage(t, pkgDir)
	levels := creationEpochLevels()
	const mixedVariantCount = 3
	require.GreaterOrEqual(t, len(levels), mixedVariantCount)
	levels = levels[:mixedVariantCount]
	stubs := []struct{ name, path string }{{launcherFullProfile, stubPath}, {launcherMinimalProfile, minimalStub}}
	for _, command := range []string{inputPackCommand, inputPGOPackCommand} {
		for _, version := range []int{1, 2} {
			for _, stub := range stubs {
				t.Run(fmt.Sprintf("%s/format%d/%s", command, version, stub.name), func(t *testing.T) {
					caseDir := t.TempDir()
					output := filepath.Join(caseDir, "app.fat")
					manifestPath := filepath.Join(caseDir, "build.json")
					index := requireRepeatedCreationEpoch(t, output, func(reverse bool) {
						variants := make([]any, 0, len(levels))
						for i, level := range levels {
							variant := map[string]any{manifestKeyLevel: level, "pgo": manifestPGOOff}
							if i == len(levels)-1 {
								variant[manifestKeyCompression] = map[string]any{manifestKeyAlgorithm: creationEpochLZ4}
							}
							variants = append(variants, variant)
						}
						if reverse {
							slices.Reverse(variants)
						}
						writeManifestJSON(t, manifestPath, map[string]any{
							"name": creationEpochApp, manifestKeyPackage: pkgDir, manifestKeyOutput: output,
							manifestKeyTargetArch: currentHostArch, manifestKeyStub: stub.path, manifestKeyVariants: variants,
							"build_flags":          []string{"-buildvcs=false", "-trimpath", qemuStripped},
							manifestKeyCompression: map[string]any{manifestKeyAlgorithm: creationEpochZstd, "dict": true},
						})
						epoch := creationEpochValue
						creationEpochCommand(t, &epoch, command, "--manifest", manifestPath,
							"--format-version", strconv.Itoa(version))
					})
					require.Len(t, index.Variants, mixedVariantCount)
					require.Positive(t, index.DictionarySize, "manifest must exercise trained dictionary bytes")
					for i, variant := range index.Variants {
						algorithm := mountCodecZstd
						if i == len(levels)-1 {
							algorithm = creationEpochLZ4
						}
						require.Equal(t, algorithm, variant.Compression)
					}
					requireEpochArtifactExecution(t, output, levels[0], creationEpochPayload)
				})
			}
		}
	}
}

func TestCreationEpochNumericMetadata(t *testing.T) {
	t.Parallel()
	level := creationEpochLevels()[0]
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("format%d", version), func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "app.fat")
			args := []string{inputPackCommand, mountArchFlag, currentHostArch, inputStubFlag, stubPath,
				creationEpochNameFlag, creationEpochApp, "--format-version", strconv.Itoa(version),
				creationEpochCompressionFlag, creationEpochNone, "-v", level + "=" + goldenVariantBins[level], "-o", output}
			var previous []byte
			for _, test := range []struct {
				epoch int64
				date  string
			}{
				{math.MinInt64, "unavailable"},
				{-1, "1969-12-31T23:59:59Z"},
				{0, "1970-01-01T00:00:00Z"},
				{math.MaxInt64, "unavailable"},
			} {
				epoch := test.epoch
				t.Run(strconv.FormatInt(epoch, 10), func(t *testing.T) {
					value := strconv.FormatInt(epoch, 10)
					creationEpochCommand(t, &value, args...)
					require.Equal(t, epoch, inspectCreationEpoch(t, output).CreatedUnix)
					inspect := exec.Command(cliPath, "inspect", output)
					inspect.Env = append(os.Environ(), "TZ=Pacific/Auckland")
					text, err := inspect.Output()
					require.NoError(t, err)
					require.Contains(t, string(text), "Created Unix:      "+value+"\n")
					require.Contains(t, string(text), "Created At:        "+test.date+"\n")
					data, err := os.ReadFile(output)
					require.NoError(t, err)
					if previous != nil {
						require.NotEqual(t, sha256.Sum256(previous), sha256.Sum256(data), "changed epoch must change artifact identity")
					}
					previous = data
					verifyFatIntegrity(t, output)
				})
			}
			before := time.Now().Unix()
			creationEpochCommand(t, nil, args...)
			after := time.Now().Unix()
			created := inspectCreationEpoch(t, output).CreatedUnix
			require.GreaterOrEqual(t, created, before)
			require.LessOrEqual(t, created, after)
		})
	}
}
