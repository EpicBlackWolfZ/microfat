//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	inputPackCommand   = "pack"
	inputVerifyCommand = "verify"
	inputStubFlag      = "--stub"
)

func TestFIFOInputCommandsTerminate(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"stub", "variant", "inspect", inputVerifyCommand, "trim", "prewarm", "manifest", "pgo-manifest"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			fifo := filepath.Join(dir, "input")
			require.NoError(t, unix.Mkfifo(fifo, uint32(privateFilePerm)))
			out := filepath.Join(dir, "output")
			require.NoError(t, os.WriteFile(out, []byte("keep"), privateFilePerm))
			args := []string{name, fifo}
			switch name {
			case "stub":
				args = []string{inputPackCommand, inputStubFlag, fifo, "-v", currentHostLevel + "=" + goldenVariantBins[currentHostLevel], "-o", out}
			case "variant":
				args = []string{inputPackCommand, inputStubFlag, stubPath, "-v", currentHostLevel + "=" + fifo, "-o", out}
			case "manifest":
				args = []string{inputPackCommand, "--manifest", fifo}
			case "pgo-manifest":
				args = []string{"pgo-pack", "--manifest", fifo}
			case "trim":
				args = append(args, "-o", out)
			}
			const timeout = 5 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			output, err := exec.CommandContext(ctx, cliPath, args...).CombinedOutput()
			require.NoError(t, ctx.Err(), "FIFO blocked: %s", output)
			require.Error(t, err, string(output))
			require.Contains(t, string(output), "regular")
			got, err := os.ReadFile(out)
			require.NoError(t, err)
			require.Equal(t, "keep", string(got))
		})
	}
}

func TestVerifyJSONExitIntegrity(t *testing.T) {
	t.Parallel()
	for _, codec := range []string{"none", "zstd", "lz4"} {
		t.Run(codec, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			fat := filepath.Join(dir, "fat")
			args := []string{inputPackCommand, inputStubFlag, stubPath, "--compression", codec, "-o", fat}
			for level, bin := range goldenVariantBins {
				args = append(args, "-v", level+"="+bin)
			}
			output, err := exec.Command(cliPath, args...).CombinedOutput()
			require.NoError(t, err, string(output))
			_, idx := readTrailerAndIndex(t, fat)
			for _, damaged := range []bool{false, true} {
				if damaged {
					data, readErr := os.ReadFile(fat)
					require.NoError(t, readErr)
					data[idx.Variants[0].Offset] ^= 0xff
					require.NoError(t, os.WriteFile(fat, data, defaultFilePerm))
				}
				for _, structured := range []bool{false, true} {
					verifyArgs := []string{inputVerifyCommand, fat}
					if structured {
						verifyArgs = append(verifyArgs, "--json")
					}
					cmd := exec.Command(cliPath, verifyArgs...)
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					err = cmd.Run()
					if damaged {
						require.Error(t, err)
						require.Contains(t, stderr.String(), "integrity verification")
					} else {
						require.NoError(t, err, stderr.String())
					}
					if structured {
						var result struct {
							Valid   bool `json:"valid"`
							Results []struct {
								Valid bool `json:"valid"`
							} `json:"results"`
						}
						require.NoError(t, json.Unmarshal(stdout.Bytes(), &result), stdout.String())
						require.Equal(t, !damaged, result.Valid)
						require.Equal(t, !damaged, result.Results[0].Valid)
						for _, other := range result.Results[1:] {
							require.True(t, other.Valid, "undamaged variants must still verify")
						}
					}
				}
			}
		})
	}
}

func TestCompiledELFInputs(t *testing.T) {
	t.Parallel()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("C compiler not installed")
	}
	for _, mode := range []string{"-no-pie", "-pie", "-static-pie", "-c", "-shared"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "main.c")
			require.NoError(t, os.WriteFile(source, []byte("int main(void) { return 0; }\n"), privateFilePerm))
			bin := filepath.Join(dir, "input")
			output, err := exec.Command(cc, mode, source, "-o", bin).CombinedOutput()
			if mode == "-static-pie" && err != nil && strings.Contains(string(output), "cannot find -lc") {
				t.Skip("static libc not installed; static PIE execution requires static libc")
			}
			require.NoError(t, err, string(output))
			fat := filepath.Join(dir, "fat")
			args := []string{inputPackCommand, inputStubFlag, stubPath, "-v", currentHostLevel + "=" + bin, "-o", fat}
			output, err = exec.Command(cliPath, args...).CombinedOutput()
			if mode == "-c" || mode == "-shared" {
				require.Error(t, err, string(output))
				require.NoFileExists(t, fat)
				return
			}
			require.NoError(t, err, string(output))
			output, err = exec.Command(cliPath, inputVerifyCommand, fat).CombinedOutput()
			require.NoError(t, err, string(output))
			for _, dispatch := range []string{"memfd", "cache"} {
				cmd := exec.Command(fat)
				cmd.Env = append(os.Environ(), "MICROFAT_EXEC_MODE="+dispatch)
				output, err = cmd.CombinedOutput()
				require.NoError(t, err, string(output))
			}
		})
	}
}
