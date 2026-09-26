package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestManifestDictionaryPrecedence(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"pack", "pgo-pack"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stubPath, err := os.Executable()
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module dictfixture\ngo 1.27.1\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
import "fmt"
func main() { fmt.Println("dictionary payload") }
`), 0o600))
			levels := []string{"v1", "v2"}
			if runtime.GOARCH == testArchARM64 {
				const upperLevel = "v8.2"
				levels = []string{"v8.0", upperLevel}
			}
			compression := map[string]any{"algorithm": "zstd", "dict": true}
			manifest := map[string]any{
				"package": ".", "output": "app-fat", "target_arch": runtime.GOARCH,
				"compression": compression, "build_flags": []string{"-trimpath"},
				"variants": []any{map[string]any{"level": levels[0]}, map[string]any{"level": levels[1]}},
			}
			const smallSize, largeSize = 1024, 4096
			cases := []struct {
				name         string
				manifestSize int
				flag         string
			}{
				{"manifest", smallSize, ""}, {"same flag", smallSize, "1024"},
				{"override", smallSize, "4096"}, {"large manifest", largeSize, ""},
				{"default", 0, ""}, {"explicit default", 0, "114688"},
			}
			digests := make(map[string]string)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					compression["dict_size"] = tc.manifestSize
					path := filepath.Join(dir, "build.json")
					encoded, err := json.Marshal(manifest)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(path, encoded, 0o600))
					args := []string{command, flagManifest, path, flagStub, stubPath}
					if tc.flag != "" {
						args = append(args, "--dict-size", tc.flag)
					}
					cmd := newRootCmd()
					var output bytes.Buffer
					cmd.SetOut(&output)
					cmd.SetErr(&output)
					cmd.SetArgs(args)
					require.NoError(t, cmd.Execute(), output.String())
					f, err := os.Open(filepath.Join(dir, "app-fat"))
					require.NoError(t, err)
					defer f.Close()
					stat, err := f.Stat()
					require.NoError(t, err)
					idx, err := format.ReadTrailerAndIndex(f, stat.Size())
					require.NoError(t, err)
					require.Positive(t, idx.DictionarySize)
					digests[tc.name] = idx.DictionarySHA256
				})
			}
			require.Equal(t, digests["manifest"], digests["same flag"])
			require.Equal(t, digests["override"], digests["large manifest"])
			require.NotEqual(t, digests["manifest"], digests["override"])
			require.Equal(t, digests["default"], digests["explicit default"])
			require.NotEqual(t, digests["manifest"], digests["default"])
		})
	}
}
