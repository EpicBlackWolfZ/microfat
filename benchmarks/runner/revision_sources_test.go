package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	revisionBaseRole = "base"
	revisionHeadRole = "head"
)

func revisionFixture(t *testing.T, name string) (string, string) {
	t.Helper()
	repository := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		stdout, stderr, err := process.Run(context.Background(), process.Spec{Path: "git", Args: args, Dir: repository})
		require.NoError(t, err, "%s", stderr)
		return strings.TrimSpace(string(stdout))
	}
	git("init", "--quiet")
	require.NoError(t, os.WriteFile(filepath.Join(repository, "source"), []byte(name), evidenceFilePerm))
	git("add", "source")
	git("-c", "user.name=Benchmark Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", name)
	return repository, git("rev-parse", "HEAD")
}

func revisionCompiler(t *testing.T, log string) string {
	t.Helper()
	compiler := filepath.Join(t.TempDir(), "go")
	script := `#!/bin/sh
set -eu
if [ "$1" = version ]; then echo 'go version go` + microfat.GoVersion() + ` linux/amd64'; exit 0; fi
output=''
while [ "$#" -gt 0 ]; do if [ "$1" = -o ]; then shift; output=$1; fi; shift; done
printf '%s\t%s\n' "$PWD" "$output" >> '` + log + `'
case "$output" in
*/head-packer|*/base-packer)
cat > "$output" <<'PACKER'
#!/bin/sh
set -eu
stub='' output=''
while [ "$#" -gt 0 ]; do
  case "$1" in --stub) shift; stub=$1;; --output) shift; output=$1;; esac
  shift
done
cp "$stub" "$output"
PACKER
chmod +x "$output"
;;
*) printf 'fixture artifact\n' > "$output";;
esac
`
	require.NoError(t, testfixture.WriteExecutable(compiler, []byte(script)))
	return compiler
}

func TestRevisionBuildSources(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"default", "independent", "dirty harness", "dirty head", "dirty base"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository, source := revisionFixture(t, "harness")
			headRepository, headSource := repository, source
			if name != "default" {
				headRepository, headSource = revisionFixture(t, "historical-head")
			}
			baseRepository, baseSource := revisionFixture(t, "historical-base")
			for role, path := range map[string]string{"harness": repository, revisionHeadRole: headRepository, revisionBaseRole: baseRepository} {
				if name == "dirty "+role {
					require.NoError(t, os.WriteFile(filepath.Join(path, "untracked"), []byte("dirty"), evidenceFilePerm))
				}
			}
			log := filepath.Join(t.TempDir(), "builds.log")
			opts := RunOptions{Repository: repository, BaseRepository: baseRepository, Go: revisionCompiler(t, log)}
			if name != "default" {
				opts.HeadRepository = headRepository
			}
			built, err := buildArtifacts(context.Background(), DefaultExperimentConfig(), opts, t.TempDir())
			require.NoError(t, err)
			assert.Equal(t, source, built.SourceSHA, "experiment identity belongs to the current harness source")
			assert.Equal(t, strings.HasPrefix(name, "dirty "), built.Dirty)
			for _, artifact := range built.Artifacts {
				expected := headSource
				if strings.HasPrefix(artifact.ID, "base-") {
					expected = baseSource
				}
				assert.Equal(t, expected, artifact.SourceSHA, artifact.ID)
			}
			data, err := os.ReadFile(log)
			require.NoError(t, err)
			var headBuilds, baseBuilds int
			for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
				fields := strings.Split(line, "\t")
				require.Len(t, fields, 2)
				expected := headRepository
				if strings.HasPrefix(filepath.Base(fields[1]), "base-") {
					expected = baseRepository
					baseBuilds++
				} else {
					headBuilds++
				}
				assert.Equal(t, expected, fields[0], "compile source for %s", fields[1])
			}
			assert.GreaterOrEqual(t, headBuilds, 3, "native payload, stub and packer builds")
			assert.Equal(t, 2, baseBuilds, "base uses shared native payloads")
		})
	}
}

func TestHeadRepositoryResolution(t *testing.T) {
	t.Parallel()
	for _, head := range []string{"", "historical-head"} {
		t.Run(head, func(t *testing.T) {
			t.Parallel()
			opts := RunOptions{Repository: "harness", HeadRepository: head, BaseRepository: revisionBaseRole,
				Fortio: "fortio", OutputDir: t.TempDir()}
			require.NoError(t, opts.resolve())
			expected := opts.Repository
			if head != "" {
				var err error
				expected, err = filepath.Abs(head)
				require.NoError(t, err)
			}
			assert.Equal(t, expected, opts.HeadRepository)
			assert.True(t, filepath.IsAbs(opts.BaseRepository))
		})
	}
}

func TestRevisionBuildRejectsUnavailableSources(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, role, failure string
	}{
		{name: "missing historical head", role: revisionHeadRole, failure: "missing"},
		{name: "historical head is a file", role: revisionHeadRole, failure: "file"},
		{name: "unreadable head index", role: revisionHeadRole, failure: "index"},
		{name: "missing historical base", role: revisionBaseRole, failure: "missing"},
		{name: "unreadable base index", role: revisionBaseRole, failure: "index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repository, _ := revisionFixture(t, "harness")
			head, _ := revisionFixture(t, "historical-head")
			base, _ := revisionFixture(t, "historical-base")
			selected := &head
			if tc.role == revisionBaseRole {
				selected = &base
			}
			switch tc.failure {
			case "missing":
				*selected = filepath.Join(t.TempDir(), "missing-checkout")
			case "file":
				*selected = filepath.Join(t.TempDir(), "checkout-file")
				require.NoError(t, os.WriteFile(*selected, []byte("not a source directory"), evidenceFilePerm))
			case "index":
				// rev-parse still resolves HEAD, but status cannot establish cleanliness.
				require.NoError(t, os.WriteFile(filepath.Join(*selected, ".git", "index"), []byte("invalid index"), evidenceFilePerm))
				sha, _, err := process.Run(context.Background(),
					process.Spec{Path: "git", Args: []string{"rev-parse", "HEAD"}, Dir: *selected})
				require.NoError(t, err)
				assert.NotEmpty(t, strings.TrimSpace(string(sha)))
			}
			log := filepath.Join(t.TempDir(), "builds.log")
			root := t.TempDir()
			built, err := buildArtifacts(context.Background(), DefaultExperimentConfig(),
				RunOptions{Repository: repository, HeadRepository: head, BaseRepository: base, Go: revisionCompiler(t, log)}, root)
			require.Error(t, err, "unavailable source or cleanliness evidence must stop artifact publication")
			assert.Nil(t, built)
			_, err = os.Stat(filepath.Join(root, "base-fat"))
			require.ErrorIs(t, err, os.ErrNotExist)
			_, err = os.Stat(log)
			if tc.role == revisionHeadRole {
				require.ErrorIs(t, err, os.ErrNotExist, "head provenance must be established before any payload build")
			} else {
				require.NoError(t, err, "base validation follows the identified head artifact build")
			}
		})
	}
}

func TestHeadRepositoryResolutionRequiresWorkingDirectory(t *testing.T) {
	// Chdir is process-wide, so keep this test serial until its cleanup restores cwd.
	directory := t.TempDir()
	t.Chdir(directory)
	require.NoError(t, os.Remove(directory))
	opts := RunOptions{Repository: "/harness", HeadRepository: "relative-head", Fortio: "/fortio",
		Helper: "/helper", OutputDir: "/output"}
	err := opts.resolve()
	require.ErrorContains(t, err, "resolving path")
	require.ErrorIs(t, err, os.ErrNotExist, "a missing cwd must not silently resolve the historical source elsewhere")
}
