package main

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"gopkg.in/yaml.v3"
)

const (
	testExecPerms = 0o755
	maxStubBytes  = 5 * 1024 * 1024
	testRepeatLen = 64
	expectedCount = 3
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
		v88Bin := compile("app_v88", srcPath, "", "GOOS=linux", "GOARCH=arm64")
		v89Bin := compile("app_v89", srcPath, "", "GOOS=linux", "GOARCH=arm64")

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
