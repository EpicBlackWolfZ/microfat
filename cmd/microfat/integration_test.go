package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

func TestEndToEndFatBinaryWorkflow(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Build real microfat-stub
	stubPath := filepath.Join(tempDir, "microfat-stub")
	buildStubCmd := exec.Command("go", "build", "-buildvcs=false", "-o", stubPath, "../microfat-stub")
	buildStubCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOAMD64=v1")
	out, err := buildStubCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build microfat-stub: %v (output: %s)", err, out)
	}

	// 2. Create and compile 3 distinct Go variant binaries
	createAndBuildVariant := func(level, greeting string) string {
		srcPath := filepath.Join(tempDir, "main_"+level+".go")
		binPath := filepath.Join(tempDir, "bin_"+level)

		code := `package main
import (
	"fmt"
	"os"
)
func main() {
	fmt.Println("` + greeting + `")
	if memLimit := os.Getenv("GOMEMLIMIT"); memLimit != "" {
		fmt.Println("ENV_GOMEMLIMIT=" + memLimit)
	}
	if maxProcs := os.Getenv("GOMAXPROCS"); maxProcs != "" {
		fmt.Println("ENV_GOMAXPROCS=" + maxProcs)
	}
}
`
		if err := os.WriteFile(srcPath, []byte(code), 0o644); err != nil {
			t.Fatalf("writing source for %s: %v", level, err)
		}

		cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, srcPath)
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		if bout, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building variant %s: %v (output: %s)", level, err, bout)
		}
		return binPath
	}

	v1Bin := createAndBuildVariant("v1", "HELLO FROM V1 BASELINE")
	v3Bin := createAndBuildVariant("v3", "HELLO FROM V3 AVX2")
	v4Bin := createAndBuildVariant("v4", "HELLO FROM V4 AVX512")

	// 3. Build microfat CLI binary
	cliPath := filepath.Join(tempDir, "microfat-cli")
	buildCliCmd := exec.Command("go", "build", "-buildvcs=false", "-o", cliPath, ".")
	buildCliCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := buildCliCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build microfat CLI: %v (output: %s)", err, out)
	}

	// 4. Run `microfat detect`
	detectCmd := exec.Command(cliPath, "detect", "--json")
	var detectBuf bytes.Buffer
	detectCmd.Stdout = &detectBuf
	if err := detectCmd.Run(); err != nil {
		t.Fatalf("microfat detect failed: %v", err)
	}
	if !strings.Contains(detectBuf.String(), `"arch": "amd64"`) {
		t.Errorf("detect output missing amd64: %s", detectBuf.String())
	}

	// 5. Run `microfat pack`
	fatBinPath := filepath.Join(tempDir, "app-fat")
	packCmd := exec.Command(cliPath, "pack",
		"--stub", stubPath,
		"--name", "demo-app",
		"-v", "v1="+v1Bin,
		"-v", "v3="+v3Bin,
		"-v", "v4="+v4Bin,
		"-o", fatBinPath,
	)
	if out, err := packCmd.CombinedOutput(); err != nil {
		t.Fatalf("microfat pack failed: %v (output: %s)", err, out)
	}

	// 6. Run `microfat inspect`
	inspectCmd := exec.Command(cliPath, "inspect", fatBinPath)
	var inspectBuf bytes.Buffer
	inspectCmd.Stdout = &inspectBuf
	if err := inspectCmd.Run(); err != nil {
		t.Fatalf("microfat inspect failed: %v", err)
	}
	inspectOut := inspectBuf.String()
	if !strings.Contains(inspectOut, "App Name:          demo-app") ||
		!strings.Contains(inspectOut, "v1") ||
		!strings.Contains(inspectOut, "v3") ||
		!strings.Contains(inspectOut, "v4") {
		t.Errorf("unexpected inspect output: %s", inspectOut)
	}

	// 7. Run `microfat verify`
	verifyCmd := exec.Command(cliPath, "verify", fatBinPath)
	if out, err := verifyCmd.CombinedOutput(); err != nil {
		t.Fatalf("microfat verify failed: %v (output: %s)", err, out)
	}

	// 8. Execute fat binary in standard transient mode (memfd_create)
	execCmd := exec.Command(fatBinPath)
	out, err = execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing fat binary failed: %v (output: %s)", err, out)
	}

	hostLevel := microarch.CurrentLevel()
	expectedOutput := "HELLO FROM V1 BASELINE"
	if microarch.Compare("amd64", hostLevel, "v4") >= 0 {
		expectedOutput = "HELLO FROM V4 AVX512"
	} else if microarch.Compare("amd64", hostLevel, "v3") >= 0 {
		expectedOutput = "HELLO FROM V3 AVX2"
	}

	if !strings.Contains(string(out), expectedOutput) {
		t.Errorf("expected output %q, got %q (host level: %s)", expectedOutput, string(out), hostLevel)
	}

	// 9. Run with explicit user GOMEMLIMIT and GOMAXPROCS and verify preservation
	explicitCmd := exec.Command(fatBinPath)
	explicitCmd.Env = append(os.Environ(), "GOMEMLIMIT=512MiB", "GOMAXPROCS=4")
	out, err = explicitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing with explicit env failed: %v (output: %s)", err, out)
	}
	if !strings.Contains(string(out), "ENV_GOMEMLIMIT=512MiB") || !strings.Contains(string(out), "ENV_GOMAXPROCS=4") {
		t.Errorf("explicit user env vars not preserved: %s", string(out))
	}

	// 10. Run `--microfat:info`
	infoCmd := exec.Command(fatBinPath, "--microfat:info")
	out, err = infoCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running --microfat:info failed: %v (output: %s)", err, out)
	}
	if !strings.Contains(string(out), "Selected Variant:") || !strings.Contains(string(out), "memfd_create") {
		t.Errorf("unexpected info output: %s", string(out))
	}

	// 11. Run `--microfat:trim-to` (creates a trimmed single-variant fat binary)
	trimmedPath := filepath.Join(tempDir, "trimmed-app")
	trimToCmd := exec.Command(fatBinPath, "--microfat:trim-to="+trimmedPath)
	if out, err := trimToCmd.CombinedOutput(); err != nil {
		t.Fatalf("running --microfat:trim-to failed: %v (output: %s)", err, out)
	}
	trimmedStat, _ := os.Stat(trimmedPath)
	fatStat, _ := os.Stat(fatBinPath)
	if trimmedStat.Size() >= fatStat.Size() {
		t.Errorf("expected trimmed size (%d) to be smaller than fat binary (%d)", trimmedStat.Size(), fatStat.Size())
	}
	// Verify executing trimmed binary still uses memfd and produces correct output
	trimExecCmd := exec.Command(trimmedPath)
	trimOut, err := trimExecCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running trimmed binary failed: %v (output: %s)", err, trimOut)
	}
	if !strings.Contains(string(trimOut), expectedOutput) {
		t.Errorf("trimmed binary produced unexpected output: %s", string(trimOut))
	}

	// 12. Run CLI `microfat trim`
	cliTrimPath := filepath.Join(tempDir, "cli-trimmed-app")
	cliTrimCmd := exec.Command(cliPath, "trim", fatBinPath, "-o", cliTrimPath)
	if out, err := cliTrimCmd.CombinedOutput(); err != nil {
		t.Fatalf("running microfat trim CLI failed: %v (output: %s)", err, out)
	}
	cliTrimExecCmd := exec.Command(cliTrimPath)
	cliTrimOut, err := cliTrimExecCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running cli trimmed binary failed: %v (output: %s)", err, cliTrimOut)
	}
	if !strings.Contains(string(cliTrimOut), expectedOutput) {
		t.Errorf("cli trimmed binary produced unexpected output: %s", string(cliTrimOut))
	}

	// 13. Run `microfat prewarm` CLI command
	prewarmCacheDir := filepath.Join(tempDir, "integ_cache")
	prewarmCliCmd := exec.Command(cliPath, "prewarm", "--cache-dir", prewarmCacheDir, "--json", fatBinPath)
	prewarmOut, err := prewarmCliCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running microfat prewarm CLI failed: %v (output: %s)", err, prewarmOut)
	}
	if !strings.Contains(string(prewarmOut), `"event": "prewarm"`) {
		t.Errorf("prewarm JSON output missing event: %s", string(prewarmOut))
	}

	// 14. Run `--microfat:prewarm` on the fat binary
	stubPrewarmCmd := exec.Command(fatBinPath, "--microfat:prewarm")
	stubPrewarmCmd.Env = append(os.Environ(), "MICROFAT_CACHE_DIR="+prewarmCacheDir)
	if out, err := stubPrewarmCmd.CombinedOutput(); err != nil {
		t.Fatalf("running --microfat:prewarm failed: %v (output: %s)", err, out)
	}

	// 15. Run fat binary with MICROFAT_EXEC_MODE=cache
	cacheExecCmd := exec.Command(fatBinPath)
	cacheExecCmd.Env = append(os.Environ(),
		"MICROFAT_CACHE_DIR="+prewarmCacheDir,
		"MICROFAT_EXEC_MODE=cache",
		"MICROFAT_DEBUG=1",
	)
	cacheExecOut, err := cacheExecCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running with MICROFAT_EXEC_MODE=cache failed: %v (output: %s)", err, cacheExecOut)
	}
	if !strings.Contains(string(cacheExecOut), expectedOutput) {
		t.Errorf("cache exec output missing expected payload output: %s", string(cacheExecOut))
	}
	if !strings.Contains(string(cacheExecOut), "exec_mode=cache") {
		t.Errorf("cache exec output missing exec_mode=cache debug telemetry: %s", string(cacheExecOut))
	}

	// 16. Run `--microfat:optimize-to`
	matPath := filepath.Join(tempDir, "materialized-app")
	matCmd := exec.Command(fatBinPath, "--microfat:optimize-to="+matPath)
	if out, err := matCmd.CombinedOutput(); err != nil {
		t.Fatalf("running --microfat:optimize-to failed: %v (output: %s)", err, out)
	}
	matExecCmd := exec.Command(matPath)
	matOut, err := matExecCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running materialized binary failed: %v (output: %s)", err, matOut)
	}
	if !strings.Contains(string(matOut), expectedOutput) {
		t.Errorf("materialized binary produced unexpected output: %s", string(matOut))
	}

	// 17. Run `--microfat:optimize` (in-place)
	statBefore, err := os.Stat(fatBinPath)
	if err != nil {
		t.Fatalf("stat fat binary: %v", err)
	}

	optCmd := exec.Command(fatBinPath, "--microfat:optimize")
	if out, err := optCmd.CombinedOutput(); err != nil {
		t.Fatalf("running --microfat:optimize in-place failed: %v (output: %s)", err, out)
	}

	statAfter, err := os.Stat(fatBinPath)
	if err != nil {
		t.Fatalf("stat optimized binary: %v", err)
	}

	if statAfter.Size() >= statBefore.Size() {
		t.Errorf("expected optimized size (%d) to be smaller than fat binary (%d)", statAfter.Size(), statBefore.Size())
	}

	// Verify the shrunk binary continues to execute perfectly
	optExecCmd := exec.Command(fatBinPath)
	optOut, err := optExecCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing optimized binary failed: %v (output: %s)", err, optOut)
	}
	if !strings.Contains(string(optOut), expectedOutput) {
		t.Errorf("optimized binary produced unexpected output: %s", string(optOut))
	}
}

func TestEndToEndPGOPackWorkflow(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Build real microfat-stub
	stubPath := filepath.Join(tempDir, "microfat-stub")
	buildStubCmd := exec.Command("go", "build", "-buildvcs=false", "-o", stubPath, "../microfat-stub")
	buildStubCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOAMD64=v1")
	out, err := buildStubCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build microfat-stub: %v (output: %s)", err, out)
	}

	// 2. Build microfat CLI binary
	cliPath := filepath.Join(tempDir, "microfat-cli")
	buildCliCmd := exec.Command("go", "build", "-buildvcs=false", "-o", cliPath, ".")
	buildCliCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := buildCliCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build microfat CLI: %v (output: %s)", err, out)
	}

	// 3. Create target application source code
	pkgDir := filepath.Join(tempDir, "myapp")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("failed to create package directory: %v", err)
	}

	srcCode := `package main

import (
	"fmt"
	"runtime"
)

func main() {
	fmt.Println("PGO_RUNNING_" + runtime.GOARCH)
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(srcCode), 0o644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module myapp\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	// 4. Create dummy profile files
	profilesDir := filepath.Join(tempDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatalf("failed to create profiles directory: %v", err)
	}
	v3PGO := filepath.Join(profilesDir, "v3.pgo")
	_ = os.WriteFile(v3PGO, []byte(""), 0o644)

	// 5. Create YAML build manifest
	manifestPath := filepath.Join(tempDir, "pgo-manifest.yaml")
	fatBinPath := filepath.Join(tempDir, "pgo-app.fat")
	manifestContent := `
name: pgo-matrix-app
package: ` + pkgDir + `
output: ` + fatBinPath + `
stub: ` + stubPath + `
target_os: linux
target_arch: amd64
variants:
  - level: v1
    pgo: "off"
  - level: v3
    pgo: ` + v3PGO + `
`
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	// 6. Run `microfat pgo-pack`
	pgoPackCmd := exec.Command(cliPath, "pgo-pack", "--manifest", manifestPath)
	pgoPackCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := pgoPackCmd.CombinedOutput(); err != nil {
		t.Fatalf("pgo-pack command failed: %v (output: %s)", err, out)
	}

	// 7. Verify generated fat binary
	verifyCmd := exec.Command(cliPath, "verify", fatBinPath)
	if out, err := verifyCmd.CombinedOutput(); err != nil {
		t.Fatalf("verify failed on pgo-pack binary: %v (output: %s)", err, out)
	}

	// 8. Inspect fat binary
	inspectCmd := exec.Command(cliPath, "inspect", fatBinPath)
	var inspectBuf bytes.Buffer
	inspectCmd.Stdout = &inspectBuf
	if err := inspectCmd.Run(); err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if !strings.Contains(inspectBuf.String(), "pgo-matrix-app") {
		t.Errorf("inspect output missing app name: %s", inspectBuf.String())
	}

	// 9. Execute fat binary
	execCmd := exec.Command(fatBinPath)
	execOut, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing pgo-pack binary failed: %v (output: %s)", err, execOut)
	}
	if !strings.Contains(string(execOut), "PGO_RUNNING_amd64") {
		t.Errorf("unexpected output from pgo fat binary: %s", string(execOut))
	}

	// 10. Run `microfat pack --manifest` shorthand
	shorthandBinPath := filepath.Join(tempDir, "shorthand-app.fat")
	shorthandPackCmd := exec.Command(cliPath, "pack", "--manifest", manifestPath, "-o", shorthandBinPath)
	shorthandPackCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := shorthandPackCmd.CombinedOutput(); err != nil {
		t.Fatalf("pack --manifest command failed: %v (output: %s)", err, out)
	}
	shorthandVerifyCmd := exec.Command(cliPath, "verify", shorthandBinPath)
	if out, err := shorthandVerifyCmd.CombinedOutput(); err != nil {
		t.Fatalf("verify failed on shorthand binary: %v (output: %s)", err, out)
	}
}

