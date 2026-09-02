package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"gopkg.in/yaml.v3"
)

const (
	testExecPerms       = 0o755
	maxStubBytes        = 5 * 1024 * 1024
	testRepeatLen       = 64
	expectedCount       = 3
	minExpectedArchives = 11
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
	if cfg.ProjectName != "microfat" {
		t.Errorf("expected project_name 'microfat', got %q", cfg.ProjectName)
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
		if err != nil {
			t.Fatalf("stat full stub: %v", err)
		}
		minStat, err := os.Stat(minStub)
		if err != nil {
			t.Fatalf("stat min stub: %v", err)
		}

		if minStat.Size() >= fullStat.Size() {
			t.Errorf("minimal stub size (%d) should be smaller than full stub size (%d)", minStat.Size(), fullStat.Size())
		}
		if minStat.Size() > int64(maxStubBytes) {
			t.Errorf("minimal stub size (%d) exceeds max ceiling (%d)", minStat.Size(), maxStubBytes)
		}

		f, err := elf.Open(minStub)
		if err != nil {
			t.Fatalf("open ELF minStub: %v", err)
		}
		defer f.Close()
		if f.Machine != elf.EM_X86_64 {
			t.Errorf("expected machine EM_X86_64, got %v", f.Machine)
		}
	})

	t.Run("ARM64_StubCrossCompilation", func(t *testing.T) {
		armStub := compile("stub-arm64", "../microfat-stub", "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.0")
		armMinStub := compile("stub-arm64-min", "../microfat-stub", "minimal", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.0")

		armStat, err := os.Stat(armStub)
		if err != nil {
			t.Fatalf("stat arm full stub: %v", err)
		}
		armMinStat, err := os.Stat(armMinStub)
		if err != nil {
			t.Fatalf("stat arm min stub: %v", err)
		}

		if armMinStat.Size() >= armStat.Size() {
			t.Errorf("minimal arm stub (%d) should be smaller than full arm stub (%d)", armMinStat.Size(), armStat.Size())
		}

		f, err := elf.Open(armMinStub)
		if err != nil {
			t.Fatalf("open arm ELF minStub: %v", err)
		}
		defer f.Close()
		if f.Machine != elf.EM_AARCH64 {
			t.Errorf("expected machine EM_AARCH64, got %v", f.Machine)
		}
	})

	t.Run("ARM64_V88_V89_MatrixPackaging", func(t *testing.T) {
		stubArm64 := compile("stub-pack-arm64", "../microfat-stub", "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.0")

		srcPath := filepath.Join(tempDir, "main.go")
		code := "package main\nfunc main() {}\n"
		if err := os.WriteFile(srcPath, []byte(code), 0o644); err != nil {
			t.Fatalf("writing dummy main.go: %v", err)
		}

		v80Bin := compile("app_v80", srcPath, "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.0")
		v88Bin := compile("app_v88", srcPath, "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.8")
		v89Bin := compile("app_v89", srcPath, "", "GOOS=linux", "GOARCH=arm64", "GOARM64=v8.9")

		// Inspect build metadata to verify GOARM64 settings
		checkARM64Setting := func(binPath, expectedLevel string) {
			t.Helper()
			bi, err := buildinfo.ReadFile(binPath)
			if err != nil {
				t.Fatalf("reading buildinfo for %s: %v", binPath, err)
			}
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
			if !found && expectedLevel != "v8.0" {
				t.Errorf("setting GOARM64 not found in %s buildinfo, expected %s", binPath, expectedLevel)
			}
		}

		checkARM64Setting(v80Bin, "v8.0")
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
			"v8.0": v80Bin,
			"v8.8": v88Bin,
			"v8.9": v89Bin,
		}

		res, err := pack.Pack(opts)
		if err != nil {
			t.Fatalf("pack failed: %v", err)
		}
		if len(res.Variants) != expectedCount {
			t.Errorf("expected %d variants, got %d", expectedCount, len(res.Variants))
		}

		fatFile, err := os.Open(outFat)
		if err != nil {
			t.Fatalf("open outFat: %v", err)
		}
		defer fatFile.Close()

		fatStat, err := fatFile.Stat()
		if err != nil {
			t.Fatalf("stat outFat: %v", err)
		}

		// Verify binary integrity
		verIdx, results, err := pack.VerifyBinary(fatFile, fatStat.Size())
		if err != nil {
			t.Fatalf("verify binary failed: %v", err)
		}
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
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == targetName {
			outF, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, testExecPerms)
			if err != nil {
				return err
			}
			defer outF.Close()
			if _, err := io.Copy(outF, tr); err != nil {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("target file %q not found in archive %s", targetName, archivePath)
}

func computeFileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func TestGoReleaserSnapshotArtifacts(t *testing.T) {
	if _, err := exec.LookPath("goreleaser"); err != nil {
		t.Skip("goreleaser not installed in PATH, skipping snapshot artifact test")
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	distDir := filepath.Join(repoRoot, "dist")
	defer func() {
		_ = os.RemoveAll(distDir)
	}()

	_, syftErr := exec.LookPath("syft")
	hasSyft := syftErr == nil

	args := []string{"release", "--snapshot", "--clean", "--skip=publish,sign,announce,validate"}
	if !hasSyft {
		args = append(args, "--skip=sbom")
	}

	cmd := exec.Command("goreleaser", args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("goreleaser snapshot failed: %v\nOutput: %s", err, string(out))
	}

	t.Run("VerifyArchivesExist", func(t *testing.T) {
		expectedPrefixes := []string{
			"microfat-stub-minimal_",
			"microfat-stub_",
			"microfat_",
		}
		for _, prefix := range expectedPrefixes {
			matches, err := filepath.Glob(filepath.Join(distDir, prefix+"*.tar.gz"))
			if err != nil {
				t.Fatalf("glob error for prefix %s: %v", prefix, err)
			}
			if len(matches) == 0 {
				t.Errorf("no archives found matching prefix %s in dist", prefix)
			}
		}

		specificArchives := []string{
			"microfat_linux_amd64_fat.tar.gz",
			"microfat_linux_arm64_fat.tar.gz",
		}
		for _, sa := range specificArchives {
			p := filepath.Join(distDir, sa)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("required archive %s missing: %v", sa, err)
			}
		}
	})

	t.Run("VerifyChecksums", func(t *testing.T) {
		checksumsPath := filepath.Join(distDir, "checksums.txt")
		f, err := os.Open(checksumsPath)
		if err != nil {
			t.Fatalf("opening checksums.txt: %v", err)
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		verifiedCount := 0
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) != 2 {
				t.Errorf("invalid checksum line format: %q", line)
				continue
			}
			expectedHash := parts[0]
			relPath := parts[1]
			fullPath := filepath.Join(distDir, relPath)

			actualHash, err := computeFileSHA256(fullPath)
			if err != nil {
				t.Errorf("computing hash for %s: %v", relPath, err)
				continue
			}
			if actualHash != expectedHash {
				t.Errorf("hash mismatch for %s: expected %s, got %s", relPath, expectedHash, actualHash)
			}
			verifiedCount++
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("scanning checksums.txt: %v", err)
		}
		if verifiedCount < minExpectedArchives {
			t.Errorf("expected at least %d verified archives in checksums.txt, got %d", minExpectedArchives, verifiedCount)
		}
	})

	t.Run("VerifySBOMs", func(t *testing.T) {
		if !hasSyft {
			t.Skip("syft not installed in PATH, skipping SBOM inspection")
		}
		spdxFiles, err := filepath.Glob(filepath.Join(distDir, "*.spdx.json"))
		if err != nil {
			t.Fatalf("globbing spdx files: %v", err)
		}
		if len(spdxFiles) == 0 {
			t.Errorf("expected .spdx.json files in dist, found none")
		}
		for _, sf := range spdxFiles {
			data, err := os.ReadFile(sf)
			if err != nil {
				t.Errorf("reading SBOM %s: %v", sf, err)
				continue
			}
			var parsed map[string]any
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Errorf("parsing SBOM %s as JSON: %v", sf, err)
			}
		}
	})

	t.Run("VerifyStubBehaviorAndSizes", func(t *testing.T) {
		fullStubArchives, err := filepath.Glob(filepath.Join(distDir, "microfat-stub_*_linux_amd64_v1.tar.gz"))
		if err != nil || len(fullStubArchives) == 0 {
			t.Fatalf("finding full stub archive: %v (found: %v)", err, fullStubArchives)
		}
		minStubArchives, err := filepath.Glob(filepath.Join(distDir, "microfat-stub-minimal_*_linux_amd64_v1.tar.gz"))
		if err != nil || len(minStubArchives) == 0 {
			t.Fatalf("finding min stub archive: %v (found: %v)", err, minStubArchives)
		}

		tempExtract := t.TempDir()
		fullStubPath := filepath.Join(tempExtract, "microfat-stub")
		minStubPath := filepath.Join(tempExtract, "microfat-stub-minimal")

		if err := extractFileFromArchive(fullStubArchives[0], "microfat-stub", fullStubPath); err != nil {
			t.Fatalf("extracting full stub: %v", err)
		}
		if err := extractFileFromArchive(minStubArchives[0], "microfat-stub-minimal", minStubPath); err != nil {
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
			if err := os.WriteFile(dummySrc, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
				t.Fatalf("writing dummy.go: %v", err)
			}
			dummyBin := filepath.Join(tempExtract, "dummy_v1")
			buildCmd := exec.Command("go", "build", "-o", dummyBin, dummySrc)
			buildCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOOS=linux", "GOARCH=amd64", "GOAMD64=v1")
			if bOut, err := buildCmd.CombinedOutput(); err != nil {
				t.Fatalf("compiling dummy: %v, out: %s", err, string(bOut))
			}

			// 1. Pack full stub fat binary
			fatFull := filepath.Join(tempExtract, "fat-full")
			optsFull := pack.DefaultOptions()
			optsFull.StubPath = fullStubPath
			optsFull.OutputPath = fatFull
			optsFull.TargetArch = "amd64"
			optsFull.AppName = "test-full-stub"
			optsFull.Variants = map[string]string{"v1": dummyBin}
			if _, err := pack.Pack(optsFull); err != nil {
				t.Fatalf("packing full stub fat binary: %v", err)
			}

			runFull := exec.Command(fatFull, "--microfat:help")
			outFull, err := runFull.CombinedOutput()
			if err != nil {
				t.Errorf("full stub --microfat:help should succeed, got %v\nOutput: %s", err, string(outFull))
			}
			if !strings.Contains(string(outFull), "microfat") {
				t.Errorf("full stub --microfat:help output missing 'microfat': %s", string(outFull))
			}

			// 2. Pack minimal stub fat binary
			fatMin := filepath.Join(tempExtract, "fat-min")
			optsMin := pack.DefaultOptions()
			optsMin.StubPath = minStubPath
			optsMin.OutputPath = fatMin
			optsMin.TargetArch = "amd64"
			optsMin.AppName = "test-min-stub"
			optsMin.Variants = map[string]string{"v1": dummyBin}
			if _, err := pack.Pack(optsMin); err != nil {
				t.Fatalf("packing min stub fat binary: %v", err)
			}

			runMin := exec.Command(fatMin, "--microfat:help")
			outMin, err := runMin.CombinedOutput()
			if err == nil {
				t.Errorf("minimal stub --microfat:help should fail, got exit 0\nOutput: %s", string(outMin))
			}
			if !strings.Contains(string(outMin), "disabled in minimal launcher stub profile") {
				t.Errorf("expected disabled message for minimal stub, got %s", string(outMin))
			}
		}
	})

	t.Run("VerifyARM64VariantBuildSettings", func(t *testing.T) {
		checkVariantGOARM64 := func(relPath, expectedSetting string) {
			t.Helper()
			p := filepath.Join(distDir, relPath)
			bi, err := buildinfo.ReadFile(p)
			if err != nil {
				t.Fatalf("reading buildinfo for %s: %v", relPath, err)
			}
			found := false
			for _, s := range bi.Settings {
				if s.Key == "GOARM64" {
					found = true
					if s.Value != expectedSetting {
						t.Errorf("for %s expected GOARM64=%s, got %s", relPath, expectedSetting, s.Value)
					}
					break
				}
			}
			if !found && expectedSetting != "v8.0" {
				t.Errorf("setting GOARM64 not found in %s, expected %s", relPath, expectedSetting)
			}
		}

		checkVariantGOARM64("microfat-arm64-v8.0_linux_arm64_v8.0/microfat", "v8.0")
		checkVariantGOARM64("microfat-arm64-v8.2_linux_arm64_v8.2/microfat", "v8.2")
		checkVariantGOARM64("microfat-arm64-v9.0_linux_arm64_v9.0/microfat", "v9.0")
	})
}
