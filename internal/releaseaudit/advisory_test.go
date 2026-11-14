package releaseaudit

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/require"
)

func TestPrepareAdvisoryAuthenticatesAndStagesBothArchitectures(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" || (runtime.GOARCH != releasecheck.ArchAMD64 && runtime.GOARCH != releasecheck.ArchARM64) {
		t.Skip("release audit requires a supported native Linux host")
	}
	options := optionsFor(t)
	options.Version = "0.3.1"
	contract, err := releasecheck.NewReleaseContract(options.Version)
	require.NoError(t, err)
	expected := buildAdvisoryFixture(t, options, contract)
	checksums := writeAdvisoryChecksums(t, options, contract)

	calls := 0
	verify := func(_ context.Context, spec process.Spec) (Result, error) {
		calls++
		require.Equal(t, "cosign", spec.Path)
		require.Contains(t, spec.Args, options.Source)
		require.Contains(t, spec.Args, "https://github.com/"+Repository+"/.github/workflows/release.yml@refs/tags/v"+options.Version)
		require.Equal(t, []string{"KEEP=yes"}, spec.Env)
		return Result{}, nil
	}
	require.NoError(t, PrepareAdvisory(t.Context(), options, []string{"KEEP=yes", "GOMAXPROCS=9"}, verify))
	require.Equal(t, 1, calls, "the artifacts must never be executed")
	for arch, payloads := range expected {
		dir := filepath.Join(options.Output, arch)
		entries, readErr := os.ReadDir(dir)
		require.NoError(t, readErr)
		require.Len(t, entries, len(payloads))
		for name, checksum := range payloads {
			path := filepath.Join(dir, name)
			actual, digestErr := Digest(path)
			require.NoError(t, digestErr)
			require.Equal(t, checksum, actual, "%s/%s", arch, name)
			info, statErr := os.Stat(path)
			require.NoError(t, statErr)
			require.Zero(t, info.Mode().Perm()&executeBits, "%s is for inspection only", path)
		}
	}
	evidenceBytes, err := os.ReadFile(filepath.Join(options.Output, "verification.json"))
	require.NoError(t, err)
	var evidence struct {
		Version string
		Source  string
		Assets  map[string]string
	}
	require.NoError(t, json.Unmarshal(evidenceBytes, &evidence))
	require.Equal(t, options.Version, evidence.Version)
	require.Equal(t, options.Source, evidence.Source)
	require.Equal(t, checksums, evidence.Assets)
	testAdvisoryStagingFailures(t, options, contract)
}

func testAdvisoryStagingFailures(t *testing.T, options Options, contract *releasecheck.ReleaseContract) {
	t.Helper()
	arch := releasecheck.ArchAMD64
	archive, err := releasecheck.ValidateArtifact(filepath.Join(options.Dist, contract.ExpectedArchives[arch]))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, archive.Cleanup()) })
	t.Run("invalid helper", func(t *testing.T) {
		invalid := options
		invalid.Dist = t.TempDir()
		require.ErrorIs(t, copyAdvisoryBinaries(invalid, contract, archive, arch), os.ErrNotExist)
	})
	t.Run("existing architecture staging directory", func(t *testing.T) {
		existing := options
		existing.Output = t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(existing.Output, arch), directoryMode))
		require.ErrorIs(t, copyAdvisoryBinaries(existing, contract, archive, arch), os.ErrExist)
	})
	t.Run("unavailable extracted executable", func(t *testing.T) {
		incomplete := *archive
		incomplete.ExtractedDir = t.TempDir()
		output := options
		output.Output = t.TempDir()
		require.ErrorIs(t, copyAdvisoryBinaries(output, contract, &incomplete, arch), os.ErrNotExist)
	})
}

func TestPrepareAdvisoryFailureBoundaries(t *testing.T) {
	t.Parallel()
	t.Run("invalid version", func(t *testing.T) {
		t.Parallel()
		options := optionsFor(t)
		options.Version = "invalid"
		require.ErrorContains(t, PrepareAdvisory(t.Context(), options, nil, nil), "invalid release version")
		require.NoDirExists(t, options.Output)
	})
	t.Run("existing output", func(t *testing.T) {
		t.Parallel()
		options := optionsFor(t)
		options.Output = t.TempDir()
		require.ErrorIs(t, PrepareAdvisory(t.Context(), options, nil, nil), os.ErrExist)
	})
	t.Run("signature rejected", func(t *testing.T) {
		t.Parallel()
		options := optionsFor(t)
		verify := executor(Result{ExitCode: 1}, nil)
		require.ErrorContains(t, PrepareAdvisory(t.Context(), options, nil, verify), "authenticating published assets")
		require.NoFileExists(t, filepath.Join(options.Output, "verification.json"))
		require.NoDirExists(t, filepath.Join(options.Output, "amd64"))
		require.NoDirExists(t, filepath.Join(options.Output, "arm64"))
	})
	t.Run("authenticated invalid archive", func(t *testing.T) {
		t.Parallel()
		options := optionsFor(t)
		signedFixture(t, options)
		require.ErrorContains(t, PrepareAdvisory(t.Context(), options, nil, executor(Result{}, nil)), "preparing amd64 published binaries")
		require.NoFileExists(t, filepath.Join(options.Output, "verification.json"))
		require.NoDirExists(t, filepath.Join(options.Output, "amd64"))
		require.NoDirExists(t, filepath.Join(options.Output, "arm64"))
	})
	t.Run("unreadable source and unwritable destination", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		source := filepath.Join(dir, "binary")
		require.ErrorIs(t, copyAdvisoryBinary(source, filepath.Join(dir, "copy")), os.ErrNotExist)
		write(t, source, "sample")
		require.Error(t, copyAdvisoryBinary(source, dir))
	})
}

func buildAdvisoryFixture(t *testing.T, options Options, contract *releasecheck.ReleaseContract) map[string]map[string]string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module github.com/EpicBlackWolfZ/microfat/cmd/microfat-install\n")
	write(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	expected := make(map[string]map[string]string)
	for _, arch := range []string{releasecheck.ArchAMD64, releasecheck.ArchARM64} {
		tiers := contract.ExpectedTiers[arch]
		opts := pack.DefaultOptions()
		opts.TargetArch = arch
		opts.Compression = codec.AlgorithmNone
		opts.AppName = releasecheck.ReleaseProjectName
		opts.OutputPath = filepath.Join(root, arch+"-fat")
		files := map[string]string{releasecheck.ReleaseProjectName: opts.OutputPath}
		for _, tier := range tiers {
			binary := buildAdvisoryBinary(t, root, arch, tier)
			opts.Variants[tier] = binary
			files[releasecheck.ReleaseProjectName+"-payload-"+tier] = binary
		}
		opts.StubPath = opts.Variants[tiers[0]]
		_, err := pack.Pack(opts)
		require.NoError(t, err)
		files[releasecheck.ReleaseFullStub] = opts.StubPath
		files[releasecheck.ReleaseMinStub] = opts.StubPath
		writeAdvisoryArchive(t, filepath.Join(options.Dist, contract.ExpectedArchives[arch]), contract.RequiredExecutables, files)
		helper, err := os.ReadFile(opts.StubPath)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(options.Dist, contract.ExpectedHelpers[arch]), helper, fileMode))
		files[releasecheck.ReleaseInstaller] = opts.StubPath
		expected[arch] = make(map[string]string)
		for name, path := range files {
			expected[arch][name], err = Digest(path)
			require.NoError(t, err)
		}
	}
	return expected
}

func buildAdvisoryBinary(t *testing.T, root, arch, tier string) string {
	t.Helper()
	binary := filepath.Join(root, arch+"-"+tier)
	cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-ldflags=-s -w", "-o", binary, ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	if arch == releasecheck.ArchAMD64 {
		cmd.Env = append(cmd.Env, "GOAMD64="+tier)
	} else {
		cmd.Env = append(cmd.Env, "GOARM64="+tier)
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return binary
}

func writeAdvisoryArchive(t *testing.T, path string, names []string, files map[string]string) {
	t.Helper()
	out, err := os.Create(path)
	require.NoError(t, err)
	compressed := gzip.NewWriter(out)
	archive := tar.NewWriter(compressed)
	for _, name := range names {
		data, readErr := os.ReadFile(files[name])
		require.NoError(t, readErr)
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}))
		_, writeErr := archive.Write(data)
		require.NoError(t, writeErr)
	}
	require.NoError(t, errors.Join(archive.Close(), compressed.Close(), out.Close()))
}

func writeAdvisoryChecksums(t *testing.T, options Options, contract *releasecheck.ReleaseContract) map[string]string {
	t.Helper()
	spdx, err := os.ReadFile("../sbom/testdata/archive.spdx.json")
	require.NoError(t, err)
	cdx, err := os.ReadFile("../sbom/testdata/archive.cdx.json")
	require.NoError(t, err)
	var rows strings.Builder
	checksums := make(map[string]string)
	for _, name := range slices.Sorted(maps.Keys(contract.ExpectedPayloadNames)) {
		path := filepath.Join(options.Dist, name)
		switch {
		case strings.HasSuffix(name, ".spdx.json"):
			require.NoError(t, os.WriteFile(path, spdx, fileMode))
		case strings.HasSuffix(name, ".cyclonedx.json"):
			require.NoError(t, os.WriteFile(path, cdx, fileMode))
		}
		checksums[name], err = Digest(path)
		require.NoError(t, err)
		fmt.Fprintf(&rows, "%s  %s\n", checksums[name], name)
	}
	write(t, filepath.Join(options.Dist, "checksums.txt"), rows.String())
	return checksums
}
