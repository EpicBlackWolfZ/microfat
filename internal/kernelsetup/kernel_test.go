package kernelsetup

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const indexRelative = "main/binary-amd64/Packages.gz"
const releaseURL = ubuntuBase + "dists/jammy-security/InRelease"
const fixtureKernel = ubuntuBase + "pool/main/kernel.deb"
const fixtureModules = ubuntuBase + "pool/main/modules.deb"

func digest(data []byte) string { value := sha256.Sum256(data); return hex.EncodeToString(value[:]) }

func compress(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := io.WriteString(writer, text)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

type fixture struct {
	lock      kernelLock
	responses map[string][]byte
	indexURL  string
	requests  []string
	client    *http.Client
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	kernel, modules := []byte("kernel image"), []byte("kernel modules")
	f := &fixture{responses: map[string][]byte{fixtureKernel: kernel, fixtureModules: modules}, client: Client()}
	f.lock = kernelLock{packagePin: packagePin{Package: "linux-image-test", Version: "1.0", URL: fixtureKernel, SHA256: digest(kernel)},
		Suite: "jammy-security", Component: "main", Architecture: "amd64",
		Modules: packagePin{Package: "linux-modules-test", Version: "1.0", URL: fixtureModules, SHA256: digest(modules)}}
	var text strings.Builder
	for _, pin := range []packagePin{f.lock.packagePin, f.lock.Modules} {
		fmt.Fprintf(&text, "Package: %s\nVersion: %s\nFilename: %s\nSHA256: %s\nDescription: test\n continuation\n\n",
			pin.Package, pin.Version, strings.TrimPrefix(pin.URL, ubuntuBase), pin.SHA256)
	}
	f.setIndex(compress(t, text.String()))
	f.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		location := req.URL.String()
		f.requests = append(f.requests, location)
		body, ok := f.responses[location]
		if !ok {
			return nil, errors.New("unexpected URL " + location)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})
	return f
}

func (f *fixture) setIndex(index []byte) {
	if f.indexURL != "" {
		delete(f.responses, f.indexURL)
	}
	f.indexURL = ubuntuBase + "dists/jammy-security/main/binary-amd64/by-hash/SHA256/" + digest(index)
	f.responses[f.indexURL] = index
	f.responses[releaseURL] = fmt.Appendf(nil, "Origin: Ubuntu\nSHA256:\n %s %d %s\nSHA512:\n ignored\n",
		digest(index), len(index), indexRelative)
}

func (f *fixture) writeLock(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "kernel.lock.json")
	data, err := json.Marshal(f.lock)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, data, fileMode))
	return file
}

func TestRunTrustOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	output := filepath.Join(t.TempDir(), "artifacts")
	require.NoError(t, Run(f.writeLock(t), output, f.client, func(file string) error {
		require.Equal(t, []string{releaseURL}, f.requests, "no index or package may be used before signature verification")
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, f.responses[releaseURL], data)
		return nil
	}))
	require.Equal(t, []string{releaseURL, f.indexURL, fixtureKernel, fixtureModules}, f.requests)
	for name, location := range map[string]string{"InRelease": releaseURL, "Packages.gz": f.indexURL,
		"kernel.deb": fixtureKernel, "modules.deb": fixtureModules} {
		data, err := os.ReadFile(filepath.Join(output, name))
		require.NoError(t, err)
		require.Equal(t, f.responses[location], data)
	}
}

func TestRunRejectsBrokenTrustChain(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"signature", "signed index", "index checksum", "index size", "gzip", "package metadata",
		"kernel checksum", "modules checksum", "release transport", "index transport", "package transport"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			verify := func(string) error { return nil }
			switch stage {
			case "signature":
				verify = func(string) error { return errors.New("invalid signature") }
			case "signed index":
				f.responses[releaseURL] = []byte("SHA256:\n")
			case "index checksum":
				f.responses[f.indexURL][0] ^= 1
			case "index size":
				f.responses[f.indexURL] = append(f.responses[f.indexURL], 1)
			case "gzip":
				f.setIndex([]byte("not gzip"))
			case "package metadata":
				f.setIndex(compress(t, "Package: other\nVersion: 1.0\n"))
			case "kernel checksum":
				f.responses[fixtureKernel] = []byte("modified kernel")
			case "modules checksum":
				f.responses[fixtureModules] = []byte("modified modules")
			case "release transport":
				delete(f.responses, releaseURL)
			case "index transport":
				delete(f.responses, f.indexURL)
			case "package transport":
				delete(f.responses, fixtureKernel)
			}
			output := t.TempDir()
			require.Error(t, Run(f.writeLock(t), output, f.client, verify))
			require.NoFileExists(t, filepath.Join(output, "modules.deb"))
			if stage == "signature" || stage == "signed index" {
				require.Len(t, f.requests, 1)
			}
			if stage != "modules checksum" {
				require.NoFileExists(t, filepath.Join(output, "kernel.deb"))
			}
		})
	}
}

func TestRunIOFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	verify := func(string) error { return nil }
	root := t.TempDir()
	require.Error(t, Run(filepath.Join(root, "missing"), root, f.client, verify))
	badLock := filepath.Join(root, "bad")
	require.NoError(t, os.WriteFile(badLock, []byte("{}"), fileMode))
	require.Error(t, Run(badLock, root, f.client, verify))
	require.Error(t, Run(f.writeLock(t), badLock, f.client, verify))
	for _, filename := range []string{"InRelease", "Packages.gz", "kernel.deb", "modules.deb"} {
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			output := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(output, filename), 0o755))
			require.Error(t, Run(f.writeLock(t), output, f.client, verify))
		})
	}
}

func TestLockValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	data, err := json.Marshal(f.lock)
	require.NoError(t, err)
	got, err := decodeLock(data)
	require.NoError(t, err)
	require.Equal(t, f.lock, got)
	for _, bad := range []string{"{", "{}", "null", strings.ReplaceAll(string(data), "jammy-security", "../other"),
		strings.ReplaceAll(string(data), f.lock.SHA256, "not a hash"), strings.ReplaceAll(string(data), "https://", "http://")} {
		_, err := decodeLock([]byte(bad))
		require.Error(t, err)
	}
	for _, bad := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		require.False(t, validDigest(bad))
	}
}

func TestURLConfinement(t *testing.T) {
	t.Parallel()
	require.NoError(t, trustedURL(releaseURL))
	for _, bad := range []string{"http://security.ubuntu.com/ubuntu/a", "file:///etc/passwd", "https://example.com/ubuntu/a",
		"https://user@security.ubuntu.com/ubuntu/a", "https://security.ubuntu.com:443/ubuntu/a",
		"https://security.ubuntu.com/ubuntu/../a", "https://security.ubuntu.com/elsewhere/a",
		"https://security.ubuntu.com/ubuntu/%2e%2e/a", releaseURL + "?q=1", releaseURL + "#fragment", "https://%"} {
		require.Error(t, trustedURL(bad), bad)
		_, err := download(Client(), bad, 1)
		require.Error(t, err)
	}
	for name, target := range map[string]string{"host": "https://example.com/ubuntu/a", "scheme": "http://security.ubuntu.com/ubuntu/a",
		"loop": releaseURL} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := Client()
			calls := 0
			client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "security.ubuntu.com", req.URL.Host)
				require.Equal(t, "https", req.URL.Scheme)
				return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")),
					Header: http.Header{"Location": {target}}}, nil
			})
			_, err := download(client, releaseURL, 1)
			require.Error(t, err)
			if name == "loop" {
				require.Equal(t, redirectLimit, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
	client := Client()
	require.Equal(t, requestTimeout, client.Timeout)
	request, err := http.NewRequest(http.MethodGet, releaseURL, nil)
	require.NoError(t, err)
	require.NoError(t, client.CheckRedirect(request, nil))
}

type brokenBody struct {
	reader   io.Reader
	closeErr error
}

func (b brokenBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b brokenBody) Close() error               { return b.closeErr }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestDownloadFailures(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]io.ReadCloser{
		"read": brokenBody{reader: brokenReader{}}, "close": brokenBody{reader: strings.NewReader("a"), closeErr: errors.New("close failed")},
		"size": io.NopCloser(strings.NewReader("ab")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := Client()
			client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})
			_, err := download(client, releaseURL, 1)
			require.Error(t, err)
		})
	}
	client := Client()
	client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("missing"))}, nil
	})
	_, err := download(client, releaseURL, 1)
	require.ErrorContains(t, err, "HTTP 404")
}

func TestSignedIndex(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("a", 64)
	valid := fmt.Sprintf("SHA256:\n %s 123 %s\nSHA512:\n ignored\n", hash, indexRelative)
	got, size, err := signedIndex([]byte(valid), indexRelative)
	require.NoError(t, err)
	require.Equal(t, hash, got)
	require.Equal(t, int64(123), size)
	for _, invalid := range []string{"", strings.ReplaceAll(valid, "SHA256:", "MD5Sum:"), valid + valid,
		strings.ReplaceAll(valid, hash, "bad"), strings.ReplaceAll(valid, "123", "bad"), strings.ReplaceAll(valid, "123", "0"),
		strings.ReplaceAll(valid, "123", "999999999"), strings.ReplaceAll(valid, indexRelative, "other/Packages.gz"),
		strings.ReplaceAll(valid, "123", "123 unexpected")} {
		_, _, err := signedIndex([]byte(invalid), indexRelative)
		require.Error(t, err)
	}
}

func TestPackageRecords(t *testing.T) {
	t.Parallel()
	data := compress(t, "\nPackage: first\nDescription: text\n continuation\n\tcontinued\n\nPackage: second\nVersion: 1\n")
	records, err := packageRecords(data)
	require.NoError(t, err)
	require.Equal(t, []map[string]string{{"Package": "first", "Description": "text"}, {"Package": "second", "Version": "1"}}, records)
	for _, text := range []string{"malformed\n", "Package: first\nPackage: second\n", "Package: \nPackage: second\n",
		": empty key\n", strings.Repeat("x", maxIndexLineBytes+1)} {
		_, err := packageRecords(compress(t, text))
		require.Error(t, err)
	}
	_, err = packageRecords([]byte("not gzip"))
	require.Error(t, err)
	_, err = packageRecords(data[:len(data)-1])
	require.Error(t, err, "gzip checksum/trailer must be verified")
	f := newFixture(t)
	records, err = packageRecords(f.responses[f.indexURL])
	require.NoError(t, err)
	require.NoError(t, verifyPackageRecord(records, f.lock.packagePin))
	require.Error(t, verifyPackageRecord(append(records, records[0]), f.lock.packagePin))
	for _, key := range []string{"SHA256", "Filename", "Package", "Version"} {
		copyRecord := make(map[string]string)
		for k, v := range records[0] {
			copyRecord[k] = v
		}
		copyRecord[key] = "mismatch"
		require.Error(t, verifyPackageRecord([]map[string]string{copyRecord}, f.lock.packagePin))
	}
}
