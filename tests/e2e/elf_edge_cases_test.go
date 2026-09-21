package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestELFEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("Scenario33_StrippedELFFatBinary", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		strippedBinPath := filepath.Join(tempDir, "stripped_v1")
		ldflags := "-ldflags=-s -w -X main.Variant=v1"
		require.NoError(t, compileBinaryWithFlags(goldenAppPkg, strippedBinPath, nil, ldflags), "compiling stripped variant")

		fatPath := filepath.Join(tempDir, "stripped.fat")
		variants := map[string]string{"v1": strippedBinPath}
		require.NoError(t, packBinary(cliPath, stubPath, "stripped-app", fatPath, variants), "packing stripped ELF binary")

		stdout, stderr, exitCode, err := executeFatBinary(t, fatPath, nil)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("executing stripped fat binary failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		if !strings.Contains(stdout, "golden:variant=v1") {
			t.Fatalf("unexpected output from stripped binary: %s", stdout)
		}
	})

	t.Run("Scenario34_PIEExecutableFatBinary", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		pieBinPath := filepath.Join(tempDir, "pie_v1")
		flags := []string{"-buildmode=pie", "-ldflags=-s -w -X main.Variant=v1"}
		require.NoError(t, compileBinaryWithFlags(goldenAppPkg, pieBinPath, nil, flags...), "compiling PIE variant")

		fatPath := filepath.Join(tempDir, "pie.fat")
		variants := map[string]string{"v1": pieBinPath}
		require.NoError(t, packBinary(cliPath, stubPath, "pie-app", fatPath, variants), "packing PIE binary")

		stdout, stderr, exitCode, err := executeFatBinary(t, fatPath, nil)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("executing PIE fat binary failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		if !strings.Contains(stdout, "golden:variant=v1") {
			t.Fatalf("unexpected output from PIE binary: %s", stdout)
		}
	})

	t.Run("Scenario35_NonELFInputRejection", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		fakeBinPath := filepath.Join(tempDir, "script.sh")
		scriptContent := "#!/bin/sh\necho 'hello world'\n"
		require.NoError(t, os.WriteFile(fakeBinPath, []byte(scriptContent), defaultFilePerm), "writing non-ELF file")

		fatPath := filepath.Join(tempDir, "non_elf.fat")
		variants := map[string]string{"v1": fakeBinPath}
		err := packBinary(cliPath, stubPath, "non-elf-app", fatPath, variants)
		require.Error(t, err, "expected microfat pack to reject non-ELF input, but succeeded")
		if !strings.Contains(err.Error(), "invalid ELF") && !strings.Contains(err.Error(), "bad magic") {
			t.Fatalf("expected invalid ELF error, got: %v", err)
		}
	})

	t.Run("Scenario36_ArchitectureMismatchRejection", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		foreignArch := archARM64
		if currentHostArch == archARM64 {
			foreignArch = archAMD64
		}

		foreignBinPath := filepath.Join(tempDir, "foreign_bin")
		envKV := []string{"GOARCH=" + foreignArch}
		ldflags := "-ldflags=-s -w -X main.Variant=v1"
		if err := compileBinaryWithFlags(goldenAppPkg, foreignBinPath, envKV, ldflags); err != nil {
			t.Skipf("cross-compiling foreign architecture %s not supported in environment: %v", foreignArch, err)
		}

		fatPath := filepath.Join(tempDir, "arch_mismatch.fat")
		variants := map[string]string{"v1": foreignBinPath}
		err := packBinary(cliPath, stubPath, "arch-mismatch-app", fatPath, variants)
		require.Error(t, err, "expected microfat pack to reject foreign architecture ELF, but succeeded")
		if !strings.Contains(err.Error(), "architecture") && !strings.Contains(err.Error(), "match") {
			t.Fatalf("expected architecture mismatch error, got: %v", err)
		}
	})
}
