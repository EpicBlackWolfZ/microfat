//go:build linux

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/homebrew"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const brewTestTap = "microfat/qualification"
const brewTestCask = brewTestTap + "/microfat"
const brewTimeout = 5 * time.Minute

type brewFixture struct {
	root, brew, tap, prefix string
	env                     []string
	t                       *testing.T
}

func (b brewFixture) command(success bool, name string, args ...string) string {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(b.t.Context(), brewTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.Dir = b.env, b.root
	output, err := cmd.CombinedOutput()
	log := fmt.Sprintf("%s %q\n%s\nerror: %v\n", name, args, output, err)
	file, fileErr := os.OpenFile(filepath.Join(b.root, "commands.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(b.t, fileErr)
	_, fileErr = file.WriteString(log)
	require.NoError(b.t, fileErr)
	require.NoError(b.t, file.Close())
	require.NoError(b.t, ctx.Err(), log)
	if success {
		require.NoError(b.t, err, log)
	} else {
		require.Error(b.t, err, log)
	}
	return string(output)
}

func newBrewFixture(t *testing.T) brewFixture {
	t.Helper()
	root := brewInput("MICROFAT_HOMEBREW_OUTPUT")
	if root == "" {
		root = t.TempDir()
	} else {
		var err error
		root, err = filepath.Abs(root)
		require.NoError(t, err)
		require.NoError(t, os.Mkdir(root, 0o700), "qualification output must be a new directory")
	}
	b := brewFixture{t: t, root: root, prefix: filepath.Join(root, "prefix"), tap: filepath.Join(root, "tap")}
	b.brew = filepath.Join(b.prefix, "Homebrew", "bin", "brew")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "HOMEBREW_") || strings.HasPrefix(key, "MICROFAT_") || key == "HOME" ||
			strings.HasPrefix(key, "XDG_") || key == "GOGC" || key == "GOMEMLIMIT" || key == "GOMAXPROCS" {
			continue
		}
		b.env = append(b.env, entry)
	}
	for key, value := range map[string]string{
		"HOME": filepath.Join(root, "home"), "XDG_CACHE_HOME": filepath.Join(root, "cache"),
		"HOMEBREW_CACHE": filepath.Join(root, "brew-cache"), "HOMEBREW_LOGS": filepath.Join(root, "brew-logs"),
		"HOMEBREW_NO_AUTO_UPDATE": "1", "HOMEBREW_NO_ANALYTICS": "1", "HOMEBREW_NO_INSTALL_FROM_API": "1",
		"HOMEBREW_NO_ENV_HINTS": "1", "HOMEBREW_DEVELOPER": "1", "HOMEBREW_NO_INSTALL_CLEANUP": "1",
		"HOMEBREW_NO_GITHUB_API": "1", "HOMEBREW_NO_COLOR": "1",
		"HOMEBREW_FORCE_VENDOR_RUBY": "1",
	} {
		b.env = append(b.env, key+"="+value)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "home"), 0o700))
	b.command(true, "git", "clone", "--quiet", "--depth=1", "--branch", homebrew.BrewVersion,
		"https://github.com/Homebrew/brew.git", filepath.Dir(filepath.Dir(b.brew)))
	actual := strings.TrimSpace(b.command(true, "git", "-C", filepath.Join(b.prefix, "Homebrew"), "rev-parse", "HEAD"))
	require.Equal(t, homebrew.BrewCommit, actual)
	require.NoError(t, os.MkdirAll(filepath.Join(b.prefix, "bin"), 0o755))
	require.NoError(t, os.Symlink("../Homebrew/bin/brew", filepath.Join(b.prefix, "bin", "brew")))
	b.brew = filepath.Join(b.prefix, "bin", "brew")
	require.Contains(t, b.command(true, b.brew, "--version"), homebrew.BrewVersion)
	require.Equal(t, b.prefix, strings.TrimSpace(b.command(true, b.brew, "--prefix")))
	require.NoError(t, os.MkdirAll(filepath.Join(b.tap, "Casks"), 0o700))
	b.command(true, "git", "init", "--quiet", "--initial-branch=main", b.tap)
	return b
}

func brewInput(name string) string {
	value := os.Getenv(name)
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(os.Getenv("MICROFAT_HOMEBREW_WORKSPACE"), value)
}

func brewArchive(t *testing.T, filename string, files map[string]string) map[string]string {
	t.Helper()
	file, err := os.Create(filename)
	require.NoError(t, err)
	gz := gzip.NewWriter(file)
	w := tar.NewWriter(gz)
	hashes := make(map[string]string)
	for _, name := range []string{releasecheck.ReleaseProjectName, releasecheck.ReleaseFullStub, releasecheck.ReleaseMinStub} {
		data, err := os.ReadFile(files[name])
		require.NoError(t, err)
		require.NoError(t, w.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}))
		_, err = w.Write(data)
		require.NoError(t, err)
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	require.NoError(t, w.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, file.Close())
	return hashes
}

func (b brewFixture) recipe(version, archive string, corrupt bool) {
	b.t.Helper()
	data, err := os.ReadFile(archive)
	require.NoError(b.t, err)
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	if corrupt {
		hash = strings.Repeat("0", 64)
	}
	data, err = homebrew.Render(homebrew.Recipe{Version: version, AMD64: hash, ARM64: hash})
	require.NoError(b.t, err)
	// Test-only local fixture transport: the production prepare command has no URL override.
	start := bytes.Index(data, []byte("  url \""))
	require.NotEqual(b.t, -1, start)
	end := start + bytes.IndexByte(data[start:], '\n')
	data = append(append(append([]byte{}, data[:start]...), []byte("  url \"file://"+archive+"\"\n")...), data[end+1:]...)
	b.commitRecipe(data)
}

func (b brewFixture) commitRecipe(data []byte) {
	b.t.Helper()
	require.NoError(b.t, os.WriteFile(filepath.Join(b.tap, "Casks", "microfat.rb"), data, 0o600))
	b.command(true, "git", "-C", b.tap, "add", "Casks/microfat.rb")
	b.command(true, "git", "-C", b.tap, "-c", "user.name=Microfat qualification", "-c", "user.email=qualification@invalid",
		"commit", "--quiet", "--allow-empty", "-m", "test: controlled cask fixture")
}

func (b brewFixture) refresh() {
	b.t.Helper()
	dir := strings.TrimSpace(b.command(true, b.brew, "--repository", brewTestTap))
	b.command(true, "git", "-C", dir, "pull", "--ff-only", "--quiet")
}

func (b brewFixture) verifyPlatforms() {
	b.t.Helper()
	// Evaluate the unmodified production template with distinct, test-only archive hashes.
	// Loading metadata does not acquire or install these unsigned fixture inputs.
	data, err := homebrew.Render(homebrew.Recipe{Version: "0.3.0", AMD64: strings.Repeat("a", 64), ARM64: strings.Repeat("b", 64)})
	require.NoError(b.t, err)
	filename := filepath.Join(b.root, "microfat.rb")
	require.NoError(b.t, os.WriteFile(filename, data, 0o600))
	metadata := b.command(true, b.brew, "ruby", "-e", `require "cask/cask_loader"; `+
		`puts Cask::CaskLoader.load(Pathname.new(ARGV.fetch(0))).to_hash_with_variations.to_json`, filename)
	// The auditor uses Homebrew's API for package-name comparisons. Disabling that API
	// would clone the complete core tap history into this otherwise isolated prefix.
	b.command(true, "env", "-u", "HOMEBREW_NO_INSTALL_FROM_API", b.brew, "ruby", "-e",
		`require "cask/cask_loader"; require "cask/auditor"; `+
			`errors = Cask::Auditor.audit(Cask::CaskLoader.load(Pathname.new(ARGV.fetch(0)))).to_a; `+
			`puts errors.to_json; exit(errors.empty? ? 0 : 1)`, filename)
	type selection struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	var result struct {
		selection
		Platforms  []string             `json:"supported_platforms"`
		Variations map[string]selection `json:"variations"`
	}
	require.NoError(b.t, json.Unmarshal([]byte(metadata), &result))
	require.ElementsMatch(b.t, []string{"x86_64_linux", "arm64_linux"}, result.Platforms)
	for platform, arch := range map[string]string{"x86_64_linux": "amd64", "arm64_linux": "arm64"} {
		value := result.Variations[platform]
		if value.URL == "" {
			value.URL = result.URL
		}
		if value.SHA256 == "" {
			value.SHA256 = result.SHA256
		}
		require.Equal(b.t, "https://github.com/EpicBlackWolfZ/microfat/releases/download/v0.3.0/"+
			"microfat_0.3.0_linux_"+arch+".tar.gz", value.URL)
		digest := strings.Repeat("a", 64)
		if arch == "arm64" {
			digest = strings.Repeat("b", 64)
		}
		require.Equal(b.t, digest, value.SHA256)
	}
}

func (b brewFixture) verify(hashes map[string]string, ownership bool) {
	b.t.Helper()
	var physical string
	for name, expected := range hashes {
		link := filepath.Join(b.prefix, "bin", name)
		info, err := os.Lstat(link)
		require.NoError(b.t, err)
		require.NotZero(b.t, info.Mode()&os.ModeSymlink)
		target, err := filepath.EvalSymlinks(link)
		require.NoError(b.t, err)
		if physical == "" {
			physical = filepath.Dir(target)
		}
		require.Equal(b.t, physical, filepath.Dir(target))
		data, err := os.ReadFile(target)
		require.NoError(b.t, err)
		require.Equal(b.t, expected, fmt.Sprintf("%x", sha256.Sum256(data)), "Homebrew changed %s", name)
	}
	marker := filepath.Join(physical, "microfat-distribution.json")
	data, err := os.ReadFile(marker)
	require.NoError(b.t, err)
	require.JSONEq(b.t, `{"schema":1,"owner":"homebrew"}`, string(data))
	info, err := os.Lstat(marker)
	require.NoError(b.t, err)
	require.True(b.t, info.Mode().IsRegular())
	require.Equal(b.t, os.FileMode(0o644), info.Mode().Perm())
	cli := filepath.Join(b.prefix, "bin", releasecheck.ReleaseProjectName)
	b.command(true, cli, "--version")
	b.command(true, "env", "PATH="+filepath.Join(b.prefix, "bin")+":"+os.Getenv("PATH"), releasecheck.ReleaseProjectName, "detect")
	for _, mode := range []string{execModeMemfd, execModeCache} {
		m := b
		m.env = append(append([]string{}, b.env...), "MICROFAT_EXEC_MODE="+mode,
			"MICROFAT_CACHE_DIR="+filepath.Join(b.root, "payload-cache"))
		for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
			packed := filepath.Join(b.root, "packed-"+mode+"-"+profile)
			m.command(true, cli, "pack", "--arch", runtime.GOARCH, "--stub-profile", profile,
				"--name", "brew-fixture", "-v", updateTier()+"="+goldenVariantBins[updateTier()], "-o", packed)
			m.command(true, packed)
		}
		if ownership {
			var result struct {
				Management    string `json:"management"`
				CanSelfUpdate bool   `json:"can_self_update"`
			}
			data := m.command(true, cli, "update", "--check", "--version", "0.2.5", "--json")
			require.NoError(b.t, json.Unmarshal([]byte(data), &result))
			assert.Equal(b.t, "homebrew", result.Management)
			assert.False(b.t, result.CanSelfUpdate)
			data = m.command(false, cli, "update", "--version", "0.2.5", "--allow-downgrade")
			assert.Contains(b.t, data, "brew upgrade microfat")
		}
	}
}

func TestHomebrewQualification(t *testing.T) {
	if os.Getenv("MICROFAT_HOMEBREW_TESTS") != "required" {
		t.Skip("disposable native Homebrew qualification is mandatory in its CI matrix")
	}
	require.NotZero(t, os.Geteuid(), "Homebrew qualification requires an unprivileged user")
	b := newBrewFixture(t)
	launchers, minimal := updateLaunchers(t)
	archive := filepath.Join(b.root, "fixture.tar.gz")
	files := map[string]string{releasecheck.ReleaseProjectName: launchers[launcherFullProfile],
		releasecheck.ReleaseFullStub: stubPath, releasecheck.ReleaseMinStub: minimal}
	hashes := brewArchive(t, archive, files)
	b.recipe("0.3.0", archive, false)
	b.command(true, b.brew, "tap", brewTestTap, "file://"+b.tap)
	b.command(true, b.brew, "style", "--cask", brewTestCask)
	b.verifyPlatforms()
	b.command(true, b.brew, "install", "--cask", brewTestCask)
	b.verify(hashes, true)
	b.command(true, b.brew, "reinstall", "--cask", brewTestCask)
	b.verify(hashes, true)
	files[releasecheck.ReleaseProjectName] = launchers[launcherMinimalProfile]
	upgraded := filepath.Join(b.root, "upgrade.tar.gz")
	upgradedHashes := brewArchive(t, upgraded, files)
	b.recipe("0.3.1", upgraded, true)
	b.refresh()
	b.command(false, b.brew, "upgrade", "--cask", brewTestCask)
	b.verify(hashes, true)
	b.recipe("0.3.1", upgraded, false)
	b.refresh()
	b.command(true, b.brew, "upgrade", "--cask", brewTestCask)
	b.verify(upgradedHashes, true)
	if dist := brewInput("MICROFAT_HOMEBREW_DIST"); dist != "" {
		version, err := releasecheck.DeriveVersion(dist, "")
		require.NoError(t, err)
		name := filepath.Join(dist, "microfat_"+version+"_linux_"+runtime.GOARCH+".tar.gz")
		name, err = filepath.Abs(name)
		require.NoError(t, err)
		facts, err := releasecheck.ValidateArtifact(name)
		require.NoError(t, err)
		defer func() { require.NoError(t, facts.Cleanup()) }()
		snapshotHashes := make(map[string]string)
		for name, fact := range facts.Executables {
			snapshotHashes[name] = fact.SHA256
		}
		b.recipe("0.3.2", name, false)
		b.refresh()
		b.command(true, b.brew, "upgrade", "--cask", brewTestCask)
		b.verify(snapshotHashes, false)
	}
	if candidate := brewInput("MICROFAT_HOMEBREW_CASK"); candidate != "" {
		require.NoError(t, homebrew.Check(t.Context(), candidate))
		data, err := os.ReadFile(candidate)
		require.NoError(t, err)
		b.commitRecipe(data)
		b.refresh()
		b.command(true, b.brew, "style", "--cask", brewTestCask)
		b.command(true, "env", "-u", "HOMEBREW_NO_INSTALL_FROM_API", b.brew, "audit", "--cask", brewTestCask)
		b.command(true, b.brew, "reinstall", "--cask", brewTestCask)
		// Recipe check authenticates archives; the published archive test below compares installed bytes.
		version, err := homebrew.CaskVersion(data)
		require.NoError(t, err)
		prepared := filepath.Join(b.root, "published")
		evidence, err := homebrew.Prepare(t.Context(), "v"+version, prepared)
		require.NoError(t, err)
		b.verify(evidence.Products[runtime.GOARCH], true)
	}
	sentinel := filepath.Join(b.prefix, "bin", "unrelated")
	require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0o600))
	b.command(true, b.brew, "uninstall", "--cask", brewTestCask)
	for _, name := range []string{releasecheck.ReleaseProjectName, releasecheck.ReleaseFullStub, releasecheck.ReleaseMinStub} {
		_, err := os.Lstat(filepath.Join(b.prefix, "bin", name))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	require.FileExists(t, sentinel)
	require.DirExists(t, filepath.Join(b.root, "payload-cache"))
	b.command(true, b.brew, "untap", brewTestTap)
	t.Logf("native %s Homebrew %s (%s); controlled fixtures are not signed releases", runtime.GOARCH,
		homebrew.BrewVersion, homebrew.BrewCommit)
}
