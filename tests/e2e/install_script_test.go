package e2e

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	installCLIName     = "microfat"
	installStubName    = "microfat-stub"
	installMinimalName = "microfat-stub-minimal"
)

func createValidReleaseTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf strings.Builder
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return []byte(buf.String())
}

func setupInstallTestServer(t *testing.T, archiveName string, archiveData []byte,
	checksumsContent, sigContent string,
) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/"+archiveName):
			if archiveData == nil {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(archiveData)
		case strings.HasSuffix(path, "/checksums.txt"):
			if checksumsContent == "" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(checksumsContent))
		case strings.HasSuffix(path, "/checksums.txt.sig"):
			if sigContent == "" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sigContent))
		default:
			http.NotFound(w, r)
		}
	})
	return httptest.NewServer(handler)
}

func setupInstallMocks(t *testing.T, cosignSuccess bool) (string, string) {
	t.Helper()
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "install.log")

	// Create mock cosign
	cosignScript := "#!/bin/sh\n"
	if cosignSuccess {
		cosignScript += "exit 0\n"
	} else {
		cosignScript += "echo 'cosign signature verification error' >&2\nexit 1\n"
	}
	cosignPath := filepath.Join(binDir, "cosign")
	require.NoError(t, os.WriteFile(cosignPath, []byte(cosignScript), 0o755))

	// Create mock install runner that logs invocations
	installScript := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %q\nexit 0\n", logPath)
	runnerPath := filepath.Join(binDir, "mock-install")
	require.NoError(t, os.WriteFile(runnerPath, []byte(installScript), 0o755))

	return binDir, logPath
}

func runInstallReleaseScript(t *testing.T, binDir, runnerPath, releaseURL, version, arch, installDir string) (int, string, string) {
	t.Helper()
	repoRoot, err := filepath.Abs("../..")
	require.NoError(t, err)
	scriptPath := filepath.Join(repoRoot, "scripts", "install-release.sh")

	cmd := exec.Command("bash", scriptPath, version, arch, installDir)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	origPath := os.Getenv("PATH")
	envPath := binDir + string(filepath.ListSeparator) + origPath
	cmd.Env = append(os.Environ(),
		"PATH="+envPath,
		"MICROFAT_RELEASE_URL="+releaseURL,
		"INSTALL_RUNNER="+runnerPath,
	)

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return exitCode, stdout.String(), stderr.String()
}

func readInstallCalls(logPath string) []string {
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var res []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			res = append(res, l)
		}
	}
	return res
}

func TestInstallScript_Regressions(t *testing.T) {
	const (
		testVersion = "0.2.3"
		testArch    = "amd64"
	)
	archiveName := fmt.Sprintf("microfat_%s_linux_%s.tar.gz", testVersion, testArch)
	validArchive := createValidReleaseTarGz(t, map[string]string{
		installCLIName:     "dummy-microfat",
		installStubName:    "dummy-stub",
		installMinimalName: "dummy-minimal",
	})
	hashBytes := sha256.Sum256(validArchive)
	validHash := hex.EncodeToString(hashBytes[:])

	standardChecksums := fmt.Sprintf("%s  %s\n%s  %s.spdx.json\n%s  %s.cyclonedx.json\n",
		validHash, archiveName,
		strings.Repeat("b", 64), archiveName,
		strings.Repeat("c", 64), archiveName,
	)

	t.Run("SignatureVerificationFailure_ZeroInstallCalls", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, false) // cosign fails
		server := setupInstallTestServer(t, archiveName, validArchive, standardChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		assert.NotEqual(t, 0, exitCode)
		assert.Contains(t, stderr, "Cosign signature verification failed")
		assert.Empty(t, readInstallCalls(logPath), "signature failure must produce zero install calls")
	})

	t.Run("MissingCosign_ZeroInstallCalls", func(t *testing.T) {
		binDir := t.TempDir()
		logPath := filepath.Join(t.TempDir(), "install.log")
		installScript := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %q\nexit 0\n", logPath)
		runnerPath := filepath.Join(binDir, "mock-install")
		require.NoError(t, os.WriteFile(runnerPath, []byte(installScript), 0o755))

		cleanPathDir := t.TempDir()
		requiredTools := []string{"bash", "sh", "mktemp", "curl", "awk", "sha256sum", "tar", "grep", "id", "rm", "printf", "cat", "mkdir"}
		for _, tool := range requiredTools {
			p, err := exec.LookPath(tool)
			if err == nil {
				_ = os.Symlink(p, filepath.Join(cleanPathDir, tool))
			}
		}

		server := setupInstallTestServer(t, archiveName, validArchive, standardChecksums, "dummy-sig")
		defer server.Close()

		repoRoot, err := filepath.Abs("../..")
		require.NoError(t, err)
		scriptPath := filepath.Join(repoRoot, "scripts", "install-release.sh")

		cmd := exec.Command("bash", scriptPath, testVersion, testArch, t.TempDir())
		var stderr strings.Builder
		cmd.Stderr = &stderr
		cmd.Env = []string{
			"PATH=" + cleanPathDir + ":" + binDir,
			"MICROFAT_RELEASE_URL=" + server.URL,
			"INSTALL_RUNNER=" + runnerPath,
		}
		err = cmd.Run()
		assert.Error(t, err)
		assert.Contains(t, stderr.String(), "cosign executable not found")
		assert.Empty(t, readInstallCalls(logPath))
	})

	t.Run("ArchiveDigestMismatch_ZeroInstallCalls", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		tamperedArchive := []byte("tampered-archive-content")
		server := setupInstallTestServer(t, archiveName, tamperedArchive, standardChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		assert.NotEqual(t, 0, exitCode)
		assert.Contains(t, stderr, "SHA-256 checksum verification failed")
		assert.Empty(t, readInstallCalls(logPath), "archive digest mismatch must produce zero install calls")
	})

	t.Run("MissingChecksumEntry_ZeroInstallCalls", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		otherChecksums := fmt.Sprintf("%s  other_archive.tar.gz\n", validHash)
		server := setupInstallTestServer(t, archiveName, validArchive, otherChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		assert.NotEqual(t, 0, exitCode)
		assert.Contains(t, stderr, "No matching checksum entry found")
		assert.Empty(t, readInstallCalls(logPath))
	})

	t.Run("DuplicateChecksumEntries_ZeroInstallCalls", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		duplicateChecksums := fmt.Sprintf("%s  %s\n%s  %s\n", validHash, archiveName, validHash, archiveName)
		server := setupInstallTestServer(t, archiveName, validArchive, duplicateChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		assert.NotEqual(t, 0, exitCode)
		assert.Contains(t, stderr, "Multiple matching checksum entries found")
		assert.Empty(t, readInstallCalls(logPath))
	})

	t.Run("MalformedChecksumHash_ZeroInstallCalls", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		malformedChecksums := fmt.Sprintf("short-hash  %s\n", archiveName)
		server := setupInstallTestServer(t, archiveName, validArchive, malformedChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		assert.NotEqual(t, 0, exitCode)
		assert.Contains(t, stderr, "Invalid checksum format")
		assert.Empty(t, readInstallCalls(logPath))
	})

	t.Run("DownloadFailure_Archive_ZeroInstallCalls", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		server := setupInstallTestServer(t, archiveName, nil, standardChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		assert.NotEqual(t, 0, exitCode)
		assert.Contains(t, stderr, "Failed to download release archive")
		assert.Empty(t, readInstallCalls(logPath))
	})

	t.Run("CorruptedArchive_ValidChecksum_ZeroInstallCalls", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		corruptedGz := []byte("not-a-valid-tar-gz-payload")
		cHash := sha256.Sum256(corruptedGz)
		corruptChecksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(cHash[:]), archiveName)
		server := setupInstallTestServer(t, archiveName, corruptedGz, corruptChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		assert.NotEqual(t, 0, exitCode)
		assert.Contains(t, stderr, "Failed to extract release archive")
		assert.Empty(t, readInstallCalls(logPath), "archive extraction failure must produce zero install calls")
	})

	t.Run("MissingRequiredBinary_ZeroInstallCalls", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		incompleteArchive := createValidReleaseTarGz(t, map[string]string{
			installCLIName:  "dummy-microfat",
			installStubName: "dummy-stub",
			// missing microfat-stub-minimal
		})
		iHash := sha256.Sum256(incompleteArchive)
		incompleteChecksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(iHash[:]), archiveName)
		server := setupInstallTestServer(t, archiveName, incompleteArchive, incompleteChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		assert.NotEqual(t, 0, exitCode)
		assert.Contains(t, stderr, "Expected binary 'microfat-stub-minimal' missing")
		assert.Empty(t, readInstallCalls(logPath))
	})

	t.Run("SuccessfulInstallation_StandardFormat", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		server := setupInstallTestServer(t, archiveName, validArchive, standardChecksums, "dummy-sig")
		defer server.Close()

		installDir := filepath.Join(t.TempDir(), "target with spaces")
		exitCode, stdout, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		require.Equal(t, 0, exitCode, "install script stdout:\n%s\nstderr:\n%s", stdout, stderr)
		assert.Contains(t, stdout, "Successfully installed microfat")

		calls := readInstallCalls(logPath)
		require.Len(t, calls, 3, "must make exactly 3 install calls for the 3 binaries")
		assert.Contains(t, calls[0], installCLIName)
		assert.Contains(t, calls[1], installStubName)
		assert.Contains(t, calls[2], installMinimalName)
	})

	t.Run("SuccessfulInstallation_BinaryChecksumMarker", func(t *testing.T) {
		binDir, logPath := setupInstallMocks(t, true)
		binaryMarkerChecksums := fmt.Sprintf("%s *%s\n%s *%s.spdx.json\n",
			validHash, archiveName,
			strings.Repeat("b", 64), archiveName,
		)
		server := setupInstallTestServer(t, archiveName, validArchive, binaryMarkerChecksums, "dummy-sig")
		defer server.Close()

		installDir := t.TempDir()
		exitCode, stdout, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
			server.URL, testVersion, testArch, installDir)

		require.Equal(t, 0, exitCode, "stderr:\n%s", stderr)
		assert.Contains(t, stdout, "Successfully installed microfat")
		calls := readInstallCalls(logPath)
		require.Len(t, calls, 3)
	})
}
