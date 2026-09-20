package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const manifestSource = `package main
import "fmt"
func main() { fmt.Println("manifest payload") }
`

func writeManifestPackage(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, defaultFilePerm))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module manifestfixture\ngo 1.27.1\n"), privateFilePerm))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(manifestSource), privateFilePerm))
}

func writeManifestJSON(t *testing.T, path string, data map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(data)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, privateFilePerm))
}

const manifestPGOOff = "off"

func TestManifestPGOPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "profile source")
	writeManifestPackage(t, profileDir)
	const profileTest = `package main
import ("crypto/sha256"; "testing"; "time")
var digest [32]byte
func TestProfile(t *testing.T) {
 end := time.Now().Add(100*time.Millisecond)
 for time.Now().Before(end) { digest = sha256.Sum256(digest[:]) }
}
`
	require.NoError(t, os.WriteFile(filepath.Join(profileDir, "main_test.go"), []byte(profileTest), privateFilePerm))
	profilePath := filepath.Join(dir, "real profile.pgo")
	profile := exec.Command("go", "test", "-run=TestProfile", "-cpuprofile="+profilePath, "-o", filepath.Join(dir, "profile.test"))
	profile.Dir = profileDir
	output, err := profile.CombinedOutput()
	require.NoError(t, err, string(output))
	profileData, err := os.ReadFile(profilePath)
	require.NoError(t, err)
	require.NotEmpty(t, profileData)
	for _, command := range []string{inputPackCommand, "pgo-pack"} {
		for _, pkg := range []string{".", "nested package"} {
			for _, mode := range []string{"variant", "default", "automatic", manifestPGOOff} {
				t.Run(command+"/"+pkg+"/"+mode, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					manifestDir := filepath.Join(root, "manifest directory")
					pkgDir := filepath.Join(manifestDir, pkg)
					writeManifestPackage(t, pkgDir)
					invocation := filepath.Join(root, "other working directory")
					require.NoError(t, os.MkdirAll(invocation, defaultFilePerm))
					variant := map[string]any{"level": currentHostLevel}
					manifest := map[string]any{
						"package": pkg, "output": "app-fat", "target_arch": currentHostArch,
						"stub": "missing-stub", "variants": []any{variant},
					}
					const profileName = "chosen profile.pgo"
					require.NoError(t, os.WriteFile(filepath.Join(manifestDir, profileName), profileData, privateFilePerm))
					switch mode {
					case "variant":
						variant["pgo"] = profileName
						manifest["default_pgo"] = "missing.pgo"
					case "default":
						manifest["default_pgo"] = profileName
					case "automatic":
						require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "default.pgo"), profileData, privateFilePerm))
					case manifestPGOOff:
						variant["pgo"] = manifestPGOOff
						manifest["default_pgo"] = "missing.pgo"
						require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "default.pgo"), []byte("invalid"), privateFilePerm))
					}
					path := filepath.Join(manifestDir, "build.json")
					writeManifestJSON(t, path, manifest)
					relative, relErr := filepath.Rel(invocation, path)
					require.NoError(t, relErr)
					for _, spelling := range []string{relative, path} {
						cmd := exec.Command(cliPath, command, "--manifest", spelling, inputStubFlag, stubPath)
						cmd.Dir = invocation
						output, runErr := cmd.CombinedOutput()
						require.NoError(t, runErr, string(output))
						applied := filepath.Join(manifestDir, profileName)
						if mode == "automatic" {
							applied = filepath.Join(pkgDir, "default.pgo")
						} else if mode == manifestPGOOff {
							applied = manifestPGOOff
						}
						require.Contains(t, string(output), "pgo: -pgo="+applied)
						fat := filepath.Join(manifestDir, "app-fat")
						output, runErr = exec.Command(cliPath, inputVerifyCommand, fat).CombinedOutput()
						require.NoError(t, runErr, string(output))
						output, runErr = exec.Command(fat).CombinedOutput()
						require.NoError(t, runErr, string(output))
						require.Contains(t, string(output), "manifest payload")
					}
				})
			}
		}
	}
}
