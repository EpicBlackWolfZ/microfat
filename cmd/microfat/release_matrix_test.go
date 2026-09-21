package main

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	releaseProjectName  = "microfat"
	releaseFullStub     = "microfat-stub"
	releaseMinStub      = "microfat-stub-minimal"
	testExecPerms       = 0o755
	maxStubBytes        = 5 * 1024 * 1024
	testRepeatLen       = 64
	expectedCount       = 3
	minExpectedArchives = 2
	arm64LevelV80       = "v8.0"
)

type goreleaserConfig struct {
	Version     int    `yaml:"version"`
	ProjectName string `yaml:"project_name"`
	Builds      []struct {
		ID     string   `yaml:"id"`
		Binary string   `yaml:"binary"`
		Goos   []string `yaml:"goos"`
		Goarch []string `yaml:"goarch"`
		Flags  []string `yaml:"flags"`
	} `yaml:"builds"`
}

func TestGoReleaserConfiguration(t *testing.T) {
	t.Parallel()

	rawYAML, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("reading .goreleaser.yaml: %v", err)
	}

	var cfg goreleaserConfig
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("parsing .goreleaser.yaml: %v", err)
	}

	if cfg.Version != 2 {
		t.Errorf("expected goreleaser version 2, got %d", cfg.Version)
	}
	if cfg.ProjectName != releaseProjectName {
		t.Errorf("expected project_name %q, got %q", releaseProjectName, cfg.ProjectName)
	}

	expectedBuildIDs := []string{
		"microfat-amd64",
		"microfat-arm64-v8.0",
		"microfat-arm64-v8.2",
		"microfat-arm64-v9.0",
		"microfat-stub-amd64",
		"microfat-stub-minimal-amd64",
		"microfat-stub-arm64",
		"microfat-stub-minimal-arm64",
	}

	foundIDs := make(map[string]bool)
	for _, b := range cfg.Builds {
		foundIDs[b.ID] = true
	}

	for _, id := range expectedBuildIDs {
		if !foundIDs[id] {
			t.Errorf("expected build ID %q in .goreleaser.yaml", id)
		}
	}
}

func TestMinimalStubAndMatrixDistribution(t *testing.T) {
	tempDir := t.TempDir()

	// Helper to build binaries with cross-compilation env
	compile := func(output, pkg string, tags string, envKV ...string) string {
		outPath := filepath.Join(tempDir, output)
		args := []string{"build", "-buildvcs=false", "-ldflags=-s -w"}
		if tags != "" {
			args = append(args, "-tags="+tags)
		}
		args = append(args, "-o", outPath, pkg)

		cmd := exec.Command("go", args...)
		env := append(os.Environ(), "GOTOOLCHAIN=local")
		env = append(env, envKV...)
		cmd.Env = env

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("compiling %s: %v\nOutput: %s", output, err, string(out))
		}
		return outPath
	}

	t.Run("AMD64_StubComparison", func(t *testing.T) {
		fullStub := compile("stub-amd64", "../microfat-stub", "", "GOOS=linux", "GOARCH=amd64", "GOAMD64=v1")
		minStub := compile("stub-amd64-min", "../microfat-stub", "minimal", "GOOS=linux", "GOARCH=amd64", "GOAMD64=v1")

		fullStat, err := os.Stat(fullStub)
		require.NoError(t, err, "stat full stub")
		minStat, err := os.Stat(minStub)
		require.NoError(t, err, "stat min stub")

		if minStat.Size() >= fullStat.Size() {
			t.Errorf("minimal stub size (%d) should be smaller than full stub size (%d)", minStat.Size(), fullStat.Size())
		}
		if minStat.Size() > int64(maxStubBytes) {
			t.Errorf("minimal stub size (%d) exceeds max ceiling (%d)", minStat.Size(), maxStubBytes)
		}

		f, err := elf.Open(minStub)
		require.NoError(t, err, "open ELF minStub")
		defer f.Close()
		if f.Machine != elf.EM_X86_64 {
			t.Errorf("expected machine EM_X86_64, got %v", f.Machine)
		}
	})

	t.Run("ARM64_StubCrossCompilation", func(t *testing.T) {
		armStub := compile("stub-arm64", "../microfat-stub", "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.0")
		armMinStub := compile("stub-arm64-min", "../microfat-stub", "minimal", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.0")

		armStat, err := os.Stat(armStub)
		require.NoError(t, err, "stat arm full stub")
		armMinStat, err := os.Stat(armMinStub)
		require.NoError(t, err, "stat arm min stub")

		if armMinStat.Size() >= armStat.Size() {
			t.Errorf("minimal arm stub (%d) should be smaller than full arm stub (%d)", armMinStat.Size(), armStat.Size())
		}

		f, err := elf.Open(armMinStub)
		require.NoError(t, err, "open arm ELF minStub")
		defer f.Close()
		if f.Machine != elf.EM_AARCH64 {
			t.Errorf("expected machine EM_AARCH64, got %v", f.Machine)
		}
	})

	t.Run("ARM64_V88_V89_MatrixPackaging", func(t *testing.T) {
		stubArm64 := compile("stub-pack-arm64", "../microfat-stub", "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.0")

		srcPath := filepath.Join(tempDir, "main.go")
		code := "package main\nfunc main() {}\n"
		require.NoError(t, os.WriteFile(srcPath, []byte(code), 0o644), "writing dummy main.go")

		v80Bin := compile("app_v80", srcPath, "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.0")
		v88Bin := compile("app_v88", srcPath, "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.8")
		v89Bin := compile("app_v89", srcPath, "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.9")

		// Inspect build metadata to verify GOARM64 settings
		checkARM64Setting := func(binPath, expectedLevel string) {
			t.Helper()
			bi, err := buildinfo.ReadFile(binPath)
			require.NoErrorf(t, err, "reading buildinfo for %s: %v", binPath, err)
			found := false
			for _, s := range bi.Settings {
				if s.Key == "GOARM64" {
					found = true
					if s.Value != expectedLevel {
						t.Errorf("expected GOARM64=%s in %s, got %s", expectedLevel, binPath, s.Value)
					}
					break
				}
			}
			if !found && expectedLevel != arm64LevelV80 {
				t.Errorf("setting GOARM64 not found in %s buildinfo, expected %s", binPath, expectedLevel)
			}
		}

		checkARM64Setting(v80Bin, arm64LevelV80)
		checkARM64Setting(v88Bin, "v8.8")
		checkARM64Setting(v89Bin, "v8.9")

		outFat := filepath.Join(tempDir, "arm64-matrix.fat")
		opts := pack.DefaultOptions()
		opts.StubPath = stubArm64
		opts.OutputPath = outFat
		opts.TargetArch = "arm64"
		opts.AppName = "arm64-matrix-app"
		opts.FormatVersion = format.FormatVersion2
		opts.Variants = map[string]string{
			arm64LevelV80: v80Bin,
			"v8.8":        v88Bin,
			"v8.9":        v89Bin,
		}

		res, err := pack.Pack(opts)
		require.NoError(t, err, "pack failed")
		if len(res.Variants) != expectedCount {
			t.Errorf("expected %d variants, got %d", expectedCount, len(res.Variants))
		}

		fatFile, err := os.Open(outFat)
		require.NoError(t, err, "open outFat")
		defer fatFile.Close()

		fatStat, err := fatFile.Stat()
		require.NoError(t, err, "stat outFat")

		// Verify binary integrity
		verIdx, results, err := pack.VerifyBinary(fatFile, fatStat.Size())
		require.NoError(t, err, "verify binary failed")
		if len(results) != expectedCount {
			t.Errorf("expected %d verification results, got %d", expectedCount, len(results))
		}
		if verIdx.Version != format.FormatVersion2 {
			t.Errorf("expected FormatVersion2, got %d", verIdx.Version)
		}
		if verIdx.TargetArch != "arm64" {
			t.Errorf("expected target arch 'arm64', got %q", verIdx.TargetArch)
		}

		// Verify SHA-256 digests exist for all variants
		for _, v := range verIdx.Variants {
			if v.SHA256 == "" {
				t.Errorf("variant %s must have SHA256 digest in Format v2", v.Level)
			}
		}
	})
}

func extractFileFromArchive(archivePath, targetName, destPath string) error {
	return releasecheck.ExtractFileFromArchive(archivePath, targetName, destPath)
}

func verifyReleaseArchivesExist(t *testing.T, distDir string) {
	t.Helper()
	specificArchives := []string{
		"microfat_*_linux_amd64.tar.gz",
		"microfat_*_linux_arm64.tar.gz",
	}
	for _, sa := range specificArchives {
		matches, err := filepath.Glob(filepath.Join(distDir, sa))
		if err != nil {
			t.Fatalf("glob error for %s: %v", sa, err)
		}
		if len(matches) != 1 {
			t.Errorf("expected exactly 1 archive matching %s, got %d", sa, len(matches))
		}
	}

	allArchives, err := filepath.Glob(filepath.Join(distDir, "*.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(allArchives) != len(specificArchives) {
		t.Errorf("expected exactly %d release archives in dist, got %d: %v", len(specificArchives), len(allArchives), allArchives)
	}
}

func verifySingleArchiveExecutables(t *testing.T, archPath string) {
	t.Helper()
	expectedArch := releasecheck.ArchAMD64
	if strings.Contains(archPath, "arm64") {
		expectedArch = releasecheck.ArchARM64
	}

	distDir := filepath.Dir(archPath)
	version, err := releasecheck.DeriveVersion(distDir, "")
	if err != nil {
		t.Fatalf("deriving release version for %s: %v", archPath, err)
	}

	contract, err := releasecheck.NewReleaseContract(version)
	if err != nil {
		t.Fatalf("creating release contract for %s: %v", archPath, err)
	}

	facts, err := releasecheck.ValidateArchive(archPath, expectedArch, contract)
	if err != nil {
		t.Fatalf("validating archive %s: %v", archPath, err)
	}
	defer func() { _ = facts.Cleanup() }()
}

func verifyReleaseArchiveRawEntries(t *testing.T, distDir string) {
	t.Helper()
	archives, err := filepath.Glob(filepath.Join(distDir, "*.tar.gz"))
	if err != nil || len(archives) == 0 {
		t.Fatalf("finding archives: %v", err)
	}
	for _, archPath := range archives {
		verifySingleArchiveExecutables(t, archPath)
	}
}

func verifyReleaseChecksums(t *testing.T, distDir string, hasSBOMTool, releaseTestsRequired bool) {
	t.Helper()
	version, err := releasecheck.DeriveVersion(distDir, "")
	if err != nil {
		t.Fatalf("deriving release version in %s: %v", distDir, err)
	}

	contract, err := releasecheck.NewReleaseContract(version)
	if err != nil {
		t.Fatalf("creating release contract: %v", err)
	}

	if !hasSBOMTool && !releaseTestsRequired {
		contract.ExpectedPayloadNames = map[string]bool{
			contract.ExpectedArchives[releasecheck.ArchAMD64]: true,
			contract.ExpectedArchives[releasecheck.ArchARM64]: true,
		}
	}

	if _, err := releasecheck.ValidateChecksums(distDir, contract); err != nil {
		t.Fatalf("validating checksums: %v", err)
	}
}

func verifyReleaseSBOMs(t *testing.T, distDir string, hasSBOMTool, releaseTestsRequired bool) {
	t.Helper()

	version, err := releasecheck.DeriveVersion(distDir, "")
	if err != nil {
		t.Fatalf("deriving release version: %v", err)
	}

	contract, err := releasecheck.NewReleaseContract(version)
	if err != nil {
		t.Fatalf("creating contract: %v", err)
	}

	if !hasSBOMTool && !releaseTestsRequired {
		spdxFiles, _ := filepath.Glob(filepath.Join(distDir, "*.spdx.json"))
		if len(spdxFiles) == 0 {
			t.Skip("cdx-convert not installed in PATH and no SBOMs generated, skipping SBOM inspection")
			return
		}
	}

	for arch, archiveName := range contract.ExpectedArchives {
		archivePath := filepath.Join(distDir, archiveName)
		facts, err := releasecheck.ValidateArchive(archivePath, arch, contract)
		if err != nil {
			t.Fatalf("validating archive %s for SBOM checks: %v", archiveName, err)
		}
		defer func(af *releasecheck.ArchiveFacts) { _ = af.Cleanup() }(facts)

		inv, err := releasecheck.ExtractArchiveInventory(facts)
		if err != nil {
			t.Fatalf("extracting inventory from archive %s: %v", archiveName, err)
		}

		spdxPath := archivePath + ".spdx.json"
		if err := releasecheck.ValidateSPDX(spdxPath, facts, inv); err != nil {
			t.Errorf("validating SPDX SBOM %s: %v", filepath.Base(spdxPath), err)
		}

		cdxPath := archivePath + ".cyclonedx.json"
		if err := releasecheck.ValidateCycloneDX(cdxPath, facts, inv); err != nil {
			t.Errorf("validating CycloneDX SBOM %s: %v", filepath.Base(cdxPath), err)
		}
	}
}

func verifyStubPrefix(t *testing.T, fatBinaryPath string, expectedStubBytes []byte) {
	t.Helper()
	fatBytes, err := os.ReadFile(fatBinaryPath)
	if err != nil {
		t.Fatalf("reading %s: %v", fatBinaryPath, err)
	}
	if len(fatBytes) < len(expectedStubBytes) {
		t.Fatalf("fat binary %s (%d bytes) smaller than stub (%d bytes)", fatBinaryPath, len(fatBytes), len(expectedStubBytes))
	}
	if !bytes.Equal(fatBytes[:len(expectedStubBytes)], expectedStubBytes) {
		t.Errorf("stub prefix in %s does not match expected stub binary bytes", fatBinaryPath)
	}
}

func verifyVariantExecutionModes(t *testing.T, microfatCliPath, fullStubPath, minStubPath, dummyBin string) {
	t.Helper()

	fullStubBytes, err := os.ReadFile(fullStubPath)
	if err != nil {
		t.Fatalf("reading full stub: %v", err)
	}

	workDir := t.TempDir()

	// Plant decoy stubs in ./bin and ../bin relative to workDir to prove they are never selected
	decoySubdir := filepath.Join(workDir, "bin")
	_ = os.MkdirAll(decoySubdir, 0o755)
	_ = os.WriteFile(filepath.Join(decoySubdir, releaseFullStub), []byte("decoy ./bin stub"), 0o755)

	decoyParent := filepath.Join(workDir, "..", "bin")
	_ = os.MkdirAll(decoyParent, 0o755)
	_ = os.WriteFile(filepath.Join(decoyParent, releaseFullStub), []byte("decoy ../bin stub"), 0o755)

	emptyPathDir := t.TempDir()
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", emptyPathDir)
	if _, err := exec.LookPath("microfat-stub"); err == nil {
		t.Fatalf("microfat-stub unexpectedly discoverable in isolated PATH environment")
	}
	t.Setenv("PATH", origPath)

	cleanEnv := []string{
		"PATH=" + emptyPathDir,
		"HOME=" + workDir,
		"TMPDIR=" + workDir,
		"GOTOOLCHAIN=local",
	}

	// 1. Forced memfd dispatch mode for the CLI without --stub
	fatMemfd := filepath.Join(workDir, "fat-memfd")
	cmdMemfd := exec.Command(microfatCliPath, "pack", "--arch", "amd64", "-v", "v1="+dummyBin, "-o", fatMemfd)
	cmdMemfd.Dir = workDir
	cmdMemfd.Env = append(append([]string(nil), cleanEnv...), "MICROFAT_EXEC_MODE=memfd")
	out, err := cmdMemfd.CombinedOutput()
	if err != nil {
		t.Fatalf("microfat pack under forced memfd failed: %v\nOutput: %s", err, string(out))
	}

	verifyStubPrefix(t, fatMemfd, fullStubBytes)

	runMemfd := exec.Command(fatMemfd)
	runMemfd.Env = append(append([]string(nil), cleanEnv...), "MICROFAT_EXEC_MODE=memfd")
	outMemfd, err := runMemfd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing fat-memfd failed: %v\nOutput: %s", err, string(outMemfd))
	}
	if !strings.Contains(string(outMemfd), "DUMMY_VARIANT_DISPATCH_OK") {
		t.Errorf("fat-memfd output missing expected marker: %s", string(outMemfd))
	}

	// 2. Forced cold cache dispatch mode for the CLI without --stub
	coldCacheDir := filepath.Join(workDir, "cache_cold")
	fatCold := filepath.Join(workDir, "fat-cold")
	cmdCold := exec.Command(microfatCliPath, "pack", "--arch", "amd64", "-v", "v1="+dummyBin, "-o", fatCold)
	cmdCold.Dir = workDir
	cmdCold.Env = append(append([]string(nil), cleanEnv...), "MICROFAT_EXEC_MODE=cache", "MICROFAT_CACHE_DIR="+coldCacheDir)
	out, err = cmdCold.CombinedOutput()
	if err != nil {
		t.Fatalf("microfat pack under forced cold cache failed: %v\nOutput: %s", err, string(out))
	}

	verifyStubPrefix(t, fatCold, fullStubBytes)

	entries, _ := os.ReadDir(coldCacheDir)
	if len(entries) == 0 {
		t.Errorf("expected cached payload in %s, found none", coldCacheDir)
	}

	// 3. Forced warm cache dispatch mode for the CLI without --stub
	fatWarm := filepath.Join(workDir, "fat-warm")
	cmdWarm := exec.Command(microfatCliPath, "pack", "--arch", "amd64", "-v", "v1="+dummyBin, "-o", fatWarm)
	cmdWarm.Dir = workDir
	cmdWarm.Env = append(append([]string(nil), cleanEnv...), "MICROFAT_EXEC_MODE=cache", "MICROFAT_CACHE_DIR="+coldCacheDir)
	out, err = cmdWarm.CombinedOutput()
	if err != nil {
		t.Fatalf("microfat pack under forced warm cache failed: %v\nOutput: %s", err, string(out))
	}

	verifyStubPrefix(t, fatWarm, fullStubBytes)

	// 4. Pack with explicit minimal stub
	fatMin := filepath.Join(workDir, "fat-min")
	cmdMin := exec.Command(microfatCliPath, "pack", "--stub", minStubPath, "--arch", "amd64", "-v", "v1="+dummyBin, "-o", fatMin)
	cmdMin.Dir = workDir
	cmdMin.Env = cleanEnv
	out, err = cmdMin.CombinedOutput()
	if err != nil {
		t.Fatalf("microfat pack with minimal stub failed: %v\nOutput: %s", err, string(out))
	}

	runMinHelp := exec.Command(fatMin, "--microfat:help")
	outMinHelp, err := runMinHelp.CombinedOutput()
	if err == nil {
		t.Errorf("minimal stub --microfat:help should fail, got exit 0: %s", string(outMinHelp))
	}
	if !strings.Contains(string(outMinHelp), "disabled in minimal launcher stub profile") {
		t.Errorf("expected disabled message for minimal stub, got %s", string(outMinHelp))
	}

	runMinExec := exec.Command(fatMin)
	runMinExec.Env = cleanEnv
	outMinExec, err := runMinExec.CombinedOutput()
	if err != nil {
		t.Fatalf("executing minimal stub payload failed: %v\nOutput: %s", err, string(outMinExec))
	}
	if !strings.Contains(string(outMinExec), "DUMMY_VARIANT_DISPATCH_OK") {
		t.Errorf("minimal stub execution missing marker: %s", string(outMinExec))
	}
}

func verifyReleaseStubBehaviorAndSizes(t *testing.T, distDir string) {
	t.Helper()
	amd64Archives, err := filepath.Glob(filepath.Join(distDir, "microfat_*_linux_amd64.tar.gz"))
	if err != nil || len(amd64Archives) == 0 {
		t.Fatalf("finding amd64 fat archive: %v (found: %v)", err, amd64Archives)
	}

	tempExtract := t.TempDir()
	fullStubPath := filepath.Join(tempExtract, releaseFullStub)
	minStubPath := filepath.Join(tempExtract, releaseMinStub)
	microfatCliPath := filepath.Join(tempExtract, releaseProjectName)

	if err := extractFileFromArchive(amd64Archives[0], releaseProjectName, microfatCliPath); err != nil {
		t.Fatalf("extracting microfat CLI from fat archive: %v", err)
	}
	if err := extractFileFromArchive(amd64Archives[0], releaseFullStub, fullStubPath); err != nil {
		t.Fatalf("extracting full stub: %v", err)
	}
	if err := extractFileFromArchive(amd64Archives[0], releaseMinStub, minStubPath); err != nil {
		t.Fatalf("extracting minimal stub: %v", err)
	}

	fullStat, err := os.Stat(fullStubPath)
	if err != nil {
		t.Fatalf("stat full stub: %v", err)
	}
	minStat, err := os.Stat(minStubPath)
	if err != nil {
		t.Fatalf("stat min stub: %v", err)
	}

	if minStat.Size() >= fullStat.Size() {
		t.Errorf("minimal stub size (%d) should be strictly smaller than full stub size (%d)", minStat.Size(), fullStat.Size())
	}

	if runtime.GOARCH == "amd64" {
		dummySrc := filepath.Join(tempExtract, "dummy.go")
		dummyCode := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"DUMMY_VARIANT_DISPATCH_OK\") }\n"
		if err := os.WriteFile(dummySrc, []byte(dummyCode), 0o644); err != nil {
			t.Fatalf("writing dummy.go: %v", err)
		}
		dummyBin := filepath.Join(tempExtract, "dummy_v1")
		buildCmd := exec.Command("go", "build", "-o", dummyBin, dummySrc)
		buildCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOOS=linux", "GOARCH=amd64", "GOAMD64=v1")
		if bOut, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("compiling dummy: %v, out: %s", err, string(bOut))
		}

		verifyVariantExecutionModes(t, microfatCliPath, fullStubPath, minStubPath, dummyBin)
	}
}

func verifyReleaseARM64Executables(t *testing.T, facts *releasecheck.ArchiveFacts, contract *releasecheck.ReleaseContract) {
	t.Helper()
	for _, req := range contract.RequiredExecutables {
		exe, ok := facts.Executables[req]
		if !ok || exe == nil {
			t.Fatalf("missing executable %s in arm64 archive", req)
		}
		if exe.ELFHeader == nil || exe.ELFHeader.Machine != elf.EM_AARCH64 {
			t.Errorf("executable %s is not an aarch64 ELF binary", req)
		}
	}
}

func verifyReleaseARM64VariantTier(t *testing.T, variant *releasecheck.VariantFacts, tier, expectedSetting string) {
	t.Helper()
	if variant.ELFHeader == nil || variant.ELFHeader.Machine != elf.EM_AARCH64 {
		t.Errorf("variant %s is not an aarch64 ELF binary", tier)
	}
	if variant.BuildInfo == nil {
		t.Fatalf("missing buildinfo in arm64 variant %s", tier)
	}

	found := false
	for _, s := range variant.BuildInfo.Settings {
		if s.Key == "GOARM64" {
			found = true
			if s.Value != expectedSetting {
				t.Errorf("variant %s GOARM64: expected %s, got %s", tier, expectedSetting, s.Value)
			}
			break
		}
	}
	if !found && expectedSetting != arm64LevelV80 {
		t.Errorf("GOARM64 setting not found for variant %s, expected %s", tier, expectedSetting)
	}
}

func verifyReleaseARM64VariantBuildSettings(t *testing.T, distDir string) {
	t.Helper()
	arm64Archives, err := filepath.Glob(filepath.Join(distDir, "microfat_*_linux_arm64.tar.gz"))
	if err != nil || len(arm64Archives) == 0 {
		t.Fatalf("finding arm64 fat archive in %s: %v", distDir, err)
	}

	version, err := releasecheck.DeriveVersion(distDir, "")
	if err != nil {
		t.Fatalf("deriving release version: %v", err)
	}

	contract, err := releasecheck.NewReleaseContract(version)
	if err != nil {
		t.Fatalf("creating contract: %v", err)
	}

	facts, err := releasecheck.ValidateArchive(arm64Archives[0], releasecheck.ArchARM64, contract)
	if err != nil {
		t.Fatalf("validating arm64 archive: %v", err)
	}
	defer func() { _ = facts.Cleanup() }()

	verifyReleaseARM64Executables(t, facts, contract)

	expectedSettings := map[string]string{
		arm64LevelV80: arm64LevelV80,
		"v8.2":        "v8.2",
		"v9.0":        "v9.0",
	}

	for tier, expectedSetting := range expectedSettings {
		variant, ok := facts.EmbeddedVariants[tier]
		if !ok || variant == nil {
			t.Fatalf("missing embedded variant %s in arm64 fat binary", tier)
		}
		verifyReleaseARM64VariantTier(t, variant, tier, expectedSetting)
	}
}

func TestGoReleaserSnapshotArtifacts(t *testing.T) {
	releaseTestsRequired := strings.EqualFold(os.Getenv("MICROFAT_RELEASE_TESTS"), "required") ||
		os.Getenv("MICROFAT_RELEASE_TESTS") == "1" ||
		strings.EqualFold(os.Getenv("MICROFAT_RELEASE_TESTS"), "true")

	var distDir string
	var hasSBOMTool bool
	customDist := os.Getenv("MICROFAT_RELEASE_DIST")
	if customDist != "" {
		absDist, err := filepath.Abs(customDist)
		if err != nil {
			t.Fatalf("resolving MICROFAT_RELEASE_DIST: %v", err)
		}
		distDir = absDist
		// Static validation-only mode: does not need goreleaser or cdx-convert installed.
		if releaseTestsRequired {
			hasSBOMTool = true
		} else {
			matches, _ := filepath.Glob(filepath.Join(distDir, "*.spdx.json"))
			hasSBOMTool = len(matches) > 0
		}
	} else {
		if _, err := exec.LookPath("goreleaser"); err != nil {
			if releaseTestsRequired {
				t.Fatalf("goreleaser required for release generation but not found in PATH: %v", err)
			}
			t.Skip("goreleaser not installed in PATH, skipping snapshot artifact test")
		}

		repoRoot, err := filepath.Abs("../..")
		if err != nil {
			t.Fatalf("resolving repo root: %v", err)
		}

		distDir = filepath.Join(repoRoot, "dist")
		t.Cleanup(func() {
			if !t.Failed() {
				_ = os.RemoveAll(distDir)
			}
		})

		args := []string{"release", "--snapshot", "--clean", "--skip=publish,sign,announce,validate"}
		_, sbomToolErr := exec.LookPath("cdx-convert")
		if sbomToolErr != nil {
			if releaseTestsRequired {
				t.Fatalf("cdx-convert required for release generation but not found in PATH: %v", sbomToolErr)
			}
			args = append(args, "--skip=sbom")
			hasSBOMTool = false
		} else {
			hasSBOMTool = true
		}

		cmd := exec.Command("goreleaser", args...)
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("goreleaser snapshot failed: %v\nOutput: %s", err, string(out))
		}
	}

	t.Run("VerifyArchivesExist", func(t *testing.T) { verifyReleaseArchivesExist(t, distDir) })
	t.Run("VerifyArchiveRawEntries", func(t *testing.T) { verifyReleaseArchiveRawEntries(t, distDir) })
	t.Run("VerifyChecksums", func(t *testing.T) { verifyReleaseChecksums(t, distDir, hasSBOMTool, releaseTestsRequired) })
	t.Run("VerifySBOMs", func(t *testing.T) { verifyReleaseSBOMs(t, distDir, hasSBOMTool, releaseTestsRequired) })
	t.Run("VerifyStubBehaviorAndSizes", func(t *testing.T) { verifyReleaseStubBehaviorAndSizes(t, distDir) })
	t.Run("VerifyARM64VariantBuildSettings", func(t *testing.T) { verifyReleaseARM64VariantBuildSettings(t, distDir) })
}

func copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func computeDirHashes(dir string) (map[string]string, error) {
	hashes := make(map[string]string)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		hashes[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	return hashes, err
}

func TestValidationOnly_DoesNotModifyCallerDistribution(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	require.NoError(t, err)

	distDir := filepath.Join(repoRoot, "dist")
	if _, err := os.Stat(distDir); os.IsNotExist(err) {
		t.Skip("dist directory not present; skipping caller distribution immutability test")
	}

	tempCopy := t.TempDir()
	require.NoError(t, copyDirectory(distDir, tempCopy))

	hashesBefore, err := computeDirHashes(tempCopy)
	require.NoError(t, err)

	t.Setenv("MICROFAT_RELEASE_DIST", tempCopy)
	t.Setenv("MICROFAT_RELEASE_TESTS", "required")

	// Run static validation subtests
	t.Run("VerifyArchivesExist", func(t *testing.T) { verifyReleaseArchivesExist(t, tempCopy) })
	t.Run("VerifyArchiveRawEntries", func(t *testing.T) { verifyReleaseArchiveRawEntries(t, tempCopy) })
	t.Run("VerifyChecksums", func(t *testing.T) { verifyReleaseChecksums(t, tempCopy, true, true) })
	t.Run("VerifySBOMs", func(t *testing.T) { verifyReleaseSBOMs(t, tempCopy, true, true) })
	t.Run("VerifyStubBehaviorAndSizes", func(t *testing.T) { verifyReleaseStubBehaviorAndSizes(t, tempCopy) })
	t.Run("VerifyARM64VariantBuildSettings", func(t *testing.T) { verifyReleaseARM64VariantBuildSettings(t, tempCopy) })

	hashesAfter, err := computeDirHashes(tempCopy)
	require.NoError(t, err)

	assert.Equal(t, hashesBefore, hashesAfter, "caller-provided distribution must never be modified by validation")
}

func TestValidationOnly_MissingSidecarFailsInRequiredMode(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	require.NoError(t, err)

	distDir := filepath.Join(repoRoot, "dist")
	if _, err := os.Stat(distDir); os.IsNotExist(err) {
		t.Skip("dist directory not present; skipping missing sidecar test")
	}

	tempCopy := t.TempDir()
	require.NoError(t, copyDirectory(distDir, tempCopy))

	// Remove one SBOM sidecar
	spdxFiles, err := filepath.Glob(filepath.Join(tempCopy, "*.spdx.json"))
	require.NoError(t, err)
	require.NotEmpty(t, spdxFiles)
	require.NoError(t, os.Remove(spdxFiles[0]))

	// Checksums verification must fail because an expected sidecar is missing
	version, err := releasecheck.DeriveVersion(tempCopy, "")
	require.NoError(t, err)
	contract, err := releasecheck.NewReleaseContract(version)
	require.NoError(t, err)

	_, err = releasecheck.ValidateChecksums(tempCopy, contract)
	assert.Error(t, err, "ValidateChecksums must fail when expected SBOM file is deleted from distribution")
}

func TestGenerationPreflight_MissingToolsFailsSubprocess(t *testing.T) {
	cmd := exec.Command("go", "test", "-v", ".", "-run", "^TestGoReleaserSnapshotArtifacts$", "-count=1")
	cmd.Env = []string{
		"PATH=/usr/bin:/bin:" + filepath.Join(runtime.GOROOT(), "bin"),
		"MICROFAT_RELEASE_TESTS=required",
		"GOPATH=" + os.Getenv("GOPATH"),
		"GOCACHE=" + os.Getenv("GOCACHE"),
		"GOTOOLCHAIN=local",
		"HOME=" + os.Getenv("HOME"),
	}
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "snapshot test in generation mode must fail when required tools are missing")
	assert.Contains(t, string(out), "required for release generation but not found in PATH")
}
