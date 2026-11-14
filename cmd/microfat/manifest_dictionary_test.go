package main

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EpicBlackWolfZ/microfat"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	subcmdPack    = "pack"
	subcmdPgoPack = "pgo-pack"
)

func TestManifestDictionaryPrecedence(t *testing.T) {
	t.Parallel()
	for _, command := range []string{subcmdPack, subcmdPgoPack} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stubPath, err := os.Executable()
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module dictfixture\ngo "+microfat.GoVersion()+"\n"), 0o600))
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

func TestDictionaryProfitabilityCLIFlagsAndDirectPack(t *testing.T) {
	t.Parallel()

	t.Run("conflicting flags rejected on pack and pgo-pack", func(t *testing.T) {
		t.Parallel()
		for _, subcmd := range []string{subcmdPack, subcmdPgoPack} {
			cmd := newRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{subcmd, "--dict", "--dict-profitable"})
			err := cmd.Execute()
			require.ErrorIs(t, err, pack.ErrConflictingDictOptions)
		}
	})

	t.Run("direct pack --dict-profitable omits unprofitable dictionary", func(t *testing.T) {
		t.Parallel()
		const noiseLen = 16 * 1024
		dir := t.TempDir()
		stubPath, err := os.Executable()
		require.NoError(t, err)

		rng := rand.New(rand.NewPCG(409, 2))
		makeVariant := func(name string) string {
			p := filepath.Join(dir, name)
			buf := make([]byte, noiseLen)
			for i := range buf {
				buf[i] = byte(rng.UintN(256))
			}
			require.NoError(t, os.WriteFile(p, buf, 0o755))
			return p
		}
		v1 := makeVariant("v1")
		v3 := makeVariant("v3")

		defaultOut := filepath.Join(dir, "size-default")
		cmdDefault := newRootCmd()
		var outDefault bytes.Buffer
		cmdDefault.SetOut(&outDefault)
		cmdDefault.SetErr(&outDefault)
		cmdDefault.SetArgs([]string{
			subcmdPack, flagStub, stubPath, flagOutput, defaultOut,
			flagProfile, codec.ProfileSize, flagSkipELF,
			"--variant", "v1=" + v1, "--variant", "v3=" + v3,
		})
		require.NoError(t, cmdDefault.Execute(), outDefault.String())

		profOut := filepath.Join(dir, "size-profitable")
		cmdProf := newRootCmd()
		var outProf bytes.Buffer
		cmdProf.SetOut(&outProf)
		cmdProf.SetErr(&outProf)
		cmdProf.SetArgs([]string{
			subcmdPack, flagStub, stubPath, flagOutput, profOut,
			flagProfile, codec.ProfileSize, "--dict-profitable", flagSkipELF,
			"--variant", "v1=" + v1, "--variant", "v3=" + v3,
		})
		require.NoError(t, cmdProf.Execute(), outProf.String())

		fDef, err := os.Open(defaultOut)
		require.NoError(t, err)
		defer fDef.Close()
		sDef, err := fDef.Stat()
		require.NoError(t, err)
		idxDef, err := format.ReadTrailerAndIndex(fDef, sDef.Size())
		require.NoError(t, err)
		assert.Positive(t, idxDef.DictionarySize)

		fProf, err := os.Open(profOut)
		require.NoError(t, err)
		defer fProf.Close()
		sProf, err := fProf.Stat()
		require.NoError(t, err)
		idxProf, err := format.ReadTrailerAndIndex(fProf, sProf.Size())
		require.NoError(t, err)
		assert.Zero(t, idxProf.DictionarySize)
		assert.Less(t, sProf.Size(), sDef.Size())
	})
}
