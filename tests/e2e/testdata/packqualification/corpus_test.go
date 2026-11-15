//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCorpusManifest(t *testing.T) {
	t.Parallel()
	var levels []string
	switch runtime.GOARCH {
	case "amd64":
		levels = []string{"v1", "v2", "v3"}
	case "arm64":
		levels = []string{"v8.0", "v8.1", "v8.2"}
	default:
		t.Skip("parallel Pack corpus is available for amd64 and arm64")
	}

	c := campaign{Corpus: t.TempDir(), Arch: runtime.GOARCH, Revision: "source", Family: "fortio"}
	profiles := []string{"stripped", "unstripped"}
	var variants []string
	for _, profile := range profiles {
		for _, level := range levels {
			data := []byte(profile + ":" + level)
			path := filepath.Join(c.Corpus, c.Family+"-"+profile+"-"+level)
			require.NoError(t, os.WriteFile(path, data, privateFile))
			hash, size, err := identify(path)
			require.NoError(t, err)
			variants = append(variants, fmt.Sprintf(
				`{"family":%q,"profile":%q,"level":%q,"path":%q,"sha256":%q,"bytes":%d}`,
				c.Family, profile, level, path, hash, size))
		}
	}

	// Match the lowercase wire keys emitted by scripts/codec-policy-corpus.sh.
	data := fmt.Sprintf(`{"schema_version":2,"go_version":%q,"architecture":%q,"source_revision":%q,"variants":[%s]}`,
		runtime.Version(), c.Arch, c.Revision, strings.Join(variants, ","))
	require.NoError(t, os.WriteFile(filepath.Join(c.Corpus, "manifest.json"), []byte(data), privateFile))

	groups, err := c.loadCorpus()
	require.NoError(t, err)
	require.Len(t, groups, len(profiles))
	for _, profile := range profiles {
		require.Len(t, groups[profile], len(levels))
		for _, level := range levels {
			assert.Equal(t, filepath.Join(c.Corpus, c.Family+"-"+profile+"-"+level), groups[profile][level])
		}
	}
}
