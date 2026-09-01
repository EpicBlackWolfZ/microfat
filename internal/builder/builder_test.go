package builder_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	testOSLinux        = "linux"
	testArchAMD64      = "amd64"
	testArchARM64      = "arm64"
	dummyELFExtraBytes = 100
	pgoOff             = "off"
)

func createDummyELF(t *testing.T, dir, name string, arch string) string {
	t.Helper()
	p := filepath.Join(dir, name)

	var elfHeader []byte
	switch arch {
	case testArchARM64:
		elfHeader = []byte{
			0x7f, 'E', 'L', 'F', 2, 1, 1, 0,
			0, 0, 0, 0, 0, 0, 0, 0,
			2, 0, 0xb7, 0x00, 1, 0, 0, 0,
		}
	default: // amd64
		elfHeader = []byte{
			0x7f, 'E', 'L', 'F', 2, 1, 1, 0,
			0, 0, 0, 0, 0, 0, 0, 0,
			2, 0, 0x3e, 0x00, 1, 0, 0, 0,
		}
	}

	payload := make([]byte, 0, len(elfHeader)+dummyELFExtraBytes)
	payload = append(payload, elfHeader...)
	payload = append(payload, make([]byte, dummyELFExtraBytes)...)
	if err := os.WriteFile(p, payload, 0o755); err != nil {
		t.Fatalf("failed to write dummy ELF %s: %v", p, err)
	}
	return p
}

func TestLoadManifest_YAML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manifestFile := filepath.Join(tmpDir, "pgo.yaml")

	content := `
name: testapp
package: ./cmd/testapp
output: bin/testapp
stub: bin/stub
target_os: linux
target_arch: amd64
default_pgo: profiles/default.pgo
build_flags:
  - "-trimpath"
tags:
  - "prod"
env:
  CGO_ENABLED: "0"
variants:
  - level: v1
    pgo: "off"
  - level: v3
    pgo: profiles/v3.pgo
    flags: ["-v"]
`
	if err := os.WriteFile(manifestFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	m, err := builder.LoadManifest(manifestFile)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}
	if m.AppName != "testapp" {
		t.Errorf("expected AppName testapp, got %s", m.AppName)
	}
	if m.Package != "./cmd/testapp" {
		t.Errorf("expected Package ./cmd/testapp, got %s", m.Package)
	}
	if m.Output != "bin/testapp" {
		t.Errorf("expected Output bin/testapp, got %s", m.Output)
	}
	if m.TargetOS != testOSLinux {
		t.Errorf("expected TargetOS linux, got %s", m.TargetOS)
	}
	if m.TargetArch != testArchAMD64 {
		t.Errorf("expected TargetArch amd64, got %s", m.TargetArch)
	}
	if m.DefaultPGO != "profiles/default.pgo" {
		t.Errorf("expected DefaultPGO profiles/default.pgo, got %s", m.DefaultPGO)
	}
	if len(m.BuildFlags) != 1 || m.BuildFlags[0] != "-trimpath" {
		t.Errorf("unexpected BuildFlags: %v", m.BuildFlags)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "prod" {
		t.Errorf("unexpected Tags: %v", m.Tags)
	}
	if m.Env["CGO_ENABLED"] != "0" {
		t.Errorf("expected CGO_ENABLED=0, got %s", m.Env["CGO_ENABLED"])
	}
	if m.Dir != tmpDir {
		t.Errorf("expected Dir %s, got %s", tmpDir, m.Dir)
	}

	if len(m.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(m.Variants))
	}
	if m.Variants[0].Level != "v1" || m.Variants[0].PGO != pgoOff {
		t.Errorf("unexpected variant 0: %+v", m.Variants[0])
	}
	if m.Variants[1].Level != "v3" || m.Variants[1].PGO != "profiles/v3.pgo" || len(m.Variants[1].Flags) != 1 {
		t.Errorf("unexpected variant 1: %+v", m.Variants[1])
	}
}

func TestLoadManifest_JSON(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manifestFile := filepath.Join(tmpDir, "pgo.json")

	mIn := builder.Manifest{
		AppName:    "jsonapp",
		Package:    ".",
		Output:     "bin/jsonapp",
		TargetOS:   testOSLinux,
		TargetArch: testArchARM64,
		Variants: []builder.VariantConfig{
			{Level: "v8.0"},
			{Level: "v8.2"},
		},
	}

	data, err := json.Marshal(mIn)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if err := os.WriteFile(manifestFile, data, 0o644); err != nil {
		t.Fatalf("failed to write json manifest: %v", err)
	}

	m, err := builder.LoadManifest(manifestFile)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}
	if m.AppName != "jsonapp" {
		t.Errorf("expected AppName jsonapp, got %s", m.AppName)
	}
	if m.TargetArch != "arm64" {
		t.Errorf("expected TargetArch arm64, got %s", m.TargetArch)
	}
	if len(m.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(m.Variants))
	}
	if m.Variants[0].Level != "v8.0" || m.Variants[1].Level != "v8.2" {
		t.Errorf("unexpected variants: %+v", m.Variants)
	}
}

func TestLoadManifest_ValidationErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	t.Run("non-existent file", func(t *testing.T) {
		_, err := builder.LoadManifest(filepath.Join(tmpDir, "missing.yaml"))
		if !errors.Is(err, builder.ErrManifestNotFound) {
			t.Errorf("expected ErrManifestNotFound, got %v", err)
		}
	})

	t.Run("invalid YAML syntax", func(t *testing.T) {
		p := filepath.Join(tmpDir, "bad.yaml")
		_ = os.WriteFile(p, []byte("name: [unclosed"), 0o644)
		_, err := builder.LoadManifest(p)
		if !errors.Is(err, builder.ErrInvalidManifest) {
			t.Errorf("expected ErrInvalidManifest, got %v", err)
		}
	})

	t.Run("invalid JSON syntax", func(t *testing.T) {
		p := filepath.Join(tmpDir, "bad.json")
		_ = os.WriteFile(p, []byte("{invalid json"), 0o644)
		_, err := builder.LoadManifest(p)
		if !errors.Is(err, builder.ErrInvalidManifest) {
			t.Errorf("expected ErrInvalidManifest, got %v", err)
		}
	})

	t.Run("unsupported architecture", func(t *testing.T) {
		p := filepath.Join(tmpDir, "unsupported_arch.yaml")
		_ = os.WriteFile(p, []byte("target_arch: mips\nvariants:\n  - level: v1\n"), 0o644)
		_, err := builder.LoadManifest(p)
		if !errors.Is(err, builder.ErrUnsupportedArch) {
			t.Errorf("expected ErrUnsupportedArch, got %v", err)
		}
	})

	t.Run("empty variants", func(t *testing.T) {
		p := filepath.Join(tmpDir, "empty_variants.yaml")
		_ = os.WriteFile(p, []byte("name: test\n"), 0o644)
		_, err := builder.LoadManifest(p)
		if !errors.Is(err, builder.ErrEmptyManifest) {
			t.Errorf("expected ErrEmptyManifest, got %v", err)
		}
	})

	t.Run("empty variant level", func(t *testing.T) {
		p := filepath.Join(tmpDir, "empty_level.yaml")
		_ = os.WriteFile(p, []byte("variants:\n  - level: \"\"\n"), 0o644)
		_, err := builder.LoadManifest(p)
		if !errors.Is(err, builder.ErrInvalidManifest) {
			t.Errorf("expected ErrInvalidManifest, got %v", err)
		}
	})

	t.Run("invalid variant level for arch", func(t *testing.T) {
		p := filepath.Join(tmpDir, "invalid_level.yaml")
		_ = os.WriteFile(p, []byte("target_arch: amd64\nvariants:\n  - level: v8.2\n"), 0o644)
		_, err := builder.LoadManifest(p)
		if !errors.Is(err, builder.ErrInvalidVariantLevel) {
			t.Errorf("expected ErrInvalidVariantLevel, got %v", err)
		}
	})

	t.Run("duplicate variant level", func(t *testing.T) {
		p := filepath.Join(tmpDir, "dup_level.yaml")
		_ = os.WriteFile(p, []byte("variants:\n  - level: v1\n  - level: amd64_v1\n"), 0o644)
		_, err := builder.LoadManifest(p)
		if !errors.Is(err, builder.ErrDuplicateVariant) {
			t.Errorf("expected ErrDuplicateVariant, got %v", err)
		}
	})
}

func TestResolveStubPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "stub.elf", testArchAMD64)

	t.Run("cli stub provided and exists", func(t *testing.T) {
		p, err := builder.ResolveStubPath(stubFile, "", "")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if p != stubFile {
			t.Errorf("expected %s, got %s", stubFile, p)
		}
	})

	t.Run("cli stub provided but missing", func(t *testing.T) {
		_, err := builder.ResolveStubPath(filepath.Join(tmpDir, "missing-stub"), "", "")
		if !errors.Is(err, builder.ErrStubNotFound) {
			t.Errorf("expected ErrStubNotFound, got %v", err)
		}
	})

	t.Run("manifest stub relative path exists", func(t *testing.T) {
		p, err := builder.ResolveStubPath("", "stub.elf", tmpDir)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if p != stubFile {
			t.Errorf("expected %s, got %s", stubFile, p)
		}
	})

	t.Run("manifest stub missing", func(t *testing.T) {
		_, err := builder.ResolveStubPath("", "nonexistent-stub", tmpDir)
		if !errors.Is(err, builder.ErrStubNotFound) {
			t.Errorf("expected ErrStubNotFound, got %v", err)
		}
	})

	t.Run("not found anywhere", func(t *testing.T) {
		_, err := builder.ResolveStubPath("", "", "")
		if err != nil && !errors.Is(err, builder.ErrStubNotFound) {
			t.Errorf("expected nil or ErrStubNotFound, got %v", err)
		}
	})
}

func TestBuildAndPack_PGOFallbacksAndErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)

	t.Run("nil manifest", func(t *testing.T) {
		_, err := builder.BuildAndPack(context.Background(), nil, builder.BuildOptions{})
		if !errors.Is(err, builder.ErrInvalidManifest) {
			t.Errorf("expected ErrInvalidManifest, got %v", err)
		}
	})

	t.Run("missing output path", func(t *testing.T) {
		m := &builder.Manifest{
			TargetArch: testArchAMD64,
			Variants:   []builder.VariantConfig{{Level: "v1"}},
		}
		_, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{StubPath: stubFile})
		if err == nil || !strings.Contains(err.Error(), "output destination path must be specified") {
			t.Errorf("expected output error, got: %v", err)
		}
	})

	t.Run("missing stub", func(t *testing.T) {
		m := &builder.Manifest{
			Output:     filepath.Join(tmpDir, "app"),
			TargetArch: testArchAMD64,
			Stub:       filepath.Join(tmpDir, "missing-stub"),
			Variants:   []builder.VariantConfig{{Level: "v1"}},
		}
		_, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{})
		if !errors.Is(err, builder.ErrStubNotFound) {
			t.Errorf("expected ErrStubNotFound, got %v", err)
		}
	})

	t.Run("missing explicit variant pgo profile", func(t *testing.T) {
		m := &builder.Manifest{
			Output:     filepath.Join(tmpDir, "app"),
			TargetArch: testArchAMD64,
			Stub:       stubFile,
			Variants: []builder.VariantConfig{
				{Level: "v1", PGO: "nonexistent.pgo"},
			},
			Dir: tmpDir,
		}
		_, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{})
		if !errors.Is(err, builder.ErrProfileNotFound) {
			t.Errorf("expected ErrProfileNotFound, got %v", err)
		}
	})

	t.Run("missing default_pgo profile", func(t *testing.T) {
		m := &builder.Manifest{
			Output:     filepath.Join(tmpDir, "app"),
			TargetArch: testArchAMD64,
			Stub:       stubFile,
			DefaultPGO: "missing_default.pgo",
			Variants: []builder.VariantConfig{
				{Level: "v1"},
			},
			Dir: tmpDir,
		}
		_, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{})
		if !errors.Is(err, builder.ErrProfileNotFound) {
			t.Errorf("expected ErrProfileNotFound, got %v", err)
		}
	})
}

func TestBuildAndPack_RealCompilation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)

	// Create a minimal compilable Go package
	pkgDir := filepath.Join(tmpDir, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("failed to create pkg dir: %v", err)
	}

	goSource := `package main

import "fmt"

func main() {
	fmt.Println("PGO compiled")
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(goSource), 0o644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module testpkg\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	// Create a dummy pgo file
	profilesDir := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatalf("failed to create profiles dir: %v", err)
	}
	dummyPGO := filepath.Join(profilesDir, "v3.pgo")
	if err := os.WriteFile(dummyPGO, []byte(""), 0o644); err != nil {
		t.Fatalf("failed to write dummy pgo: %v", err)
	}

	outFile := filepath.Join(tmpDir, "bin", "myfatbin")

	m := &builder.Manifest{
		AppName:    "pgo-demo",
		Package:    pkgDir,
		Output:     outFile,
		Stub:       stubFile,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []builder.VariantConfig{
			{Level: "v1", PGO: pgoOff},
			{Level: "v3", PGO: dummyPGO},
		},
		Dir: tmpDir,
	}

	var stdoutBuf bytes.Buffer
	res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
		Concurrency:       2,
		KeepIntermediates: true,
		SkipELFValidation: false,
		Stdout:            &stdoutBuf,
	})
	if err != nil {
		t.Fatalf("BuildAndPack failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil BuildResult")
	}
	if res.OutputPath != outFile {
		t.Errorf("expected OutputPath %s, got %s", outFile, res.OutputPath)
	}
	if len(res.Index.Variants) != 2 {
		t.Fatalf("expected 2 variants in index, got %d", len(res.Index.Variants))
	}

	// Verify the produced fat binary
	f, err := os.Open(outFile)
	if err != nil {
		t.Fatalf("failed to open output binary: %v", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if !format.IsFatBinary(f, stat.Size()) {
		t.Errorf("expected valid fat binary magic trailer")
	}

	idx, results, err := pack.VerifyBinary(f, stat.Size())
	if err != nil {
		t.Fatalf("VerifyBinary failed: %v", err)
	}
	if idx.AppName != "pgo-demo" {
		t.Errorf("expected AppName pgo-demo, got %s", idx.AppName)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.Valid {
			t.Errorf("variant %s failed verification: %v", r.Level, r.Error)
		}
	}

	// Verify intermediates directory exists because KeepIntermediates was true
	if _, err := os.Stat(res.IntermediatesDir); err != nil {
		t.Errorf("expected intermediates dir to exist: %v", err)
	}
}

func TestBuildAndPack_PackageDefaultPGO(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)

	pkgDir := filepath.Join(tmpDir, "pkg2")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("failed to create pkg dir: %v", err)
	}

	goSource := `package main
func main() {}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(goSource), 0o644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module testpkg2\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	// Place default.pgo in the package dir
	if err := os.WriteFile(filepath.Join(pkgDir, "default.pgo"), []byte(""), 0o644); err != nil {
		t.Fatalf("failed to write default.pgo: %v", err)
	}

	outFile := filepath.Join(tmpDir, "bin", "myfatbin2")

	m := &builder.Manifest{
		AppName:    "pkg-default-pgo",
		Package:    pkgDir,
		Output:     outFile,
		Stub:       stubFile,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []builder.VariantConfig{
			{Level: "v1"}, // Unspecified -> should pick up default.pgo in pkgDir
		},
		Dir: tmpDir,
	}

	res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
		Concurrency:       1,
		KeepIntermediates: false,
	})
	if err != nil {
		t.Fatalf("BuildAndPack failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil BuildResult")
	}
	if !strings.Contains(res.VariantPGOApplied["v1"], "default.pgo") {
		t.Errorf("expected default.pgo in applied PGO, got %s", res.VariantPGOApplied["v1"])
	}
}

func TestBuildAndPack_CompilationFailure(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)

	// Package with syntax error
	pkgDir := filepath.Join(tmpDir, "badpkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("failed to create pkg dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte("package main\nfunc invalid syntax"), 0o644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module badpkg\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	outFile := filepath.Join(tmpDir, "bin", "badbin")

	m := &builder.Manifest{
		AppName:    "badapp",
		Package:    pkgDir,
		Output:     outFile,
		Stub:       stubFile,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []builder.VariantConfig{
			{Level: "v1"},
		},
		Dir: tmpDir,
	}

	_, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{})
	if err == nil || !strings.Contains(err.Error(), "compiling variant v1") {
		t.Errorf("expected compilation error, got: %v", err)
	}
}

func TestBuildAndPack_RelativePathsAndDistinctDirs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)

	// Create package in a relative subdirectory
	pkgDir := filepath.Join(tmpDir, "src", "pkgapp")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("failed to create pkg dir: %v", err)
	}
	mainCode := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"ok\") }\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(mainCode), 0o644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module pkgapp\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	m := &builder.Manifest{
		AppName:    "relapp",
		Package:    "./src/pkgapp",
		Output:     "dist/bin/relfat",
		Stub:       stubFile,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []builder.VariantConfig{
			{Level: "v1", PGO: pgoOff},
			{Level: "v3", PGO: pgoOff},
		},
		Dir: tmpDir,
	}

	res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
		Concurrency:       2,
		KeepIntermediates: false,
	})
	if err != nil {
		t.Fatalf("BuildAndPack with relative paths failed: %v", err)
	}
	if res == nil || res.Index == nil {
		t.Fatalf("expected valid BuildResult and Index")
	}
	if len(res.Index.Variants) != 2 {
		t.Errorf("expected 2 variants, got %d", len(res.Index.Variants))
	}
}

func TestLoadManifest_CompressionValidation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	validContent := `
name: compapp
package: .
output: bin/compapp
stub: bin/stub
target_os: linux
target_arch: amd64
compression:
  profile: latency
  algorithm: lz4
  level: fastest
variants:
  - level: v1
    compression:
      algorithm: none
  - level: v3
    compression:
      algorithm: zstd
      level: best
`
	validFile := filepath.Join(tmpDir, "valid.yaml")
	_ = os.WriteFile(validFile, []byte(validContent), 0o644)

	m, err := builder.LoadManifest(validFile)
	if err != nil {
		t.Fatalf("LoadManifest valid compression failed: %v", err)
	}
	if m.Compression == nil || m.Compression.Profile != "latency" || m.Compression.Algorithm != "lz4" {
		t.Fatalf("unexpected root compression: %+v", m.Compression)
	}
	if len(m.Variants) != 2 || m.Variants[0].Compression.Algorithm != "none" || m.Variants[1].Compression.Algorithm != "zstd" {
		t.Fatalf("unexpected variant compression: %+v", m.Variants)
	}

	// Invalid root profile
	badProfileContent := `
package: .
variants: [{level: v1}]
compression:
  profile: invalid_profile
`
	badProfileFile := filepath.Join(tmpDir, "bad_prof.yaml")
	_ = os.WriteFile(badProfileFile, []byte(badProfileContent), 0o644)
	if _, err := builder.LoadManifest(badProfileFile); err == nil {
		t.Errorf("expected error on invalid compression profile")
	}

	// Invalid variant algorithm
	badAlgoContent := `
package: .
variants:
  - level: v1
    compression:
      algorithm: invalid_algo
`
	badAlgoFile := filepath.Join(tmpDir, "bad_algo.yaml")
	_ = os.WriteFile(badAlgoFile, []byte(badAlgoContent), 0o644)
	if _, err := builder.LoadManifest(badAlgoFile); err == nil {
		t.Errorf("expected error on invalid variant compression algorithm")
	}
}

func TestBuildAndPack_CompressionProfiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)

	pkgDir := filepath.Join(tmpDir, "src", "compapp")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("failed to create pkg dir: %v", err)
	}
	mainCode := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"comp test\") }\n"
	_ = os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(mainCode), 0o644)
	_ = os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module compapp\ngo 1.27.1\n"), 0o644)

	m := &builder.Manifest{
		AppName:    "compfat",
		Package:    "./src/compapp",
		Output:     "dist/bin/compfat",
		Stub:       stubFile,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Compression: &builder.CompressionConfig{
			Profile:   "latency",
			Algorithm: "lz4",
		},
		Variants: []builder.VariantConfig{
			{
				Level: "v1",
				PGO:   pgoOff,
				Compression: &builder.CompressionConfig{
					Algorithm: "none",
				},
			},
			{
				Level: "v3",
				PGO:   pgoOff,
			},
		},
		Dir: tmpDir,
	}

	res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
		Concurrency: 2,
	})
	if err != nil {
		t.Fatalf("BuildAndPack with compression failed: %v", err)
	}

	v1, _ := res.Index.FindVariant("v1")
	if v1.Compression != "none" {
		t.Errorf("expected v1 to be 'none', got %q", v1.Compression)
	}
	v3, _ := res.Index.FindVariant("v3")
	if v3.Compression != "lz4" {
		t.Errorf("expected v3 to inherit 'lz4', got %q", v3.Compression)
	}
}


