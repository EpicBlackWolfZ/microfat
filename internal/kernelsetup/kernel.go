// Package kernelsetup verifies benchmark guest packages against Ubuntu-signed metadata.
package kernelsetup

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ubuntuBase            = "https://security.ubuntu.com/ubuntu/"
	maxReleaseBytes       = 4 << 20
	maxIndexBytes         = 64 << 20
	maxPackageBytes       = 256 << 20
	maxExpandedIndexBytes = 128 << 20
	maxIndexLineBytes     = 1 << 20
	requestTimeout        = 60 * time.Second
	redirectLimit         = 5
	fileMode              = 0o600
	directoryMode         = 0o700
	fieldCount            = 3
)

var segmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type packagePin struct {
	Package string `json:"package"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

type kernelLock struct {
	packagePin
	Suite        string     `json:"suite"`
	Component    string     `json:"component"`
	Architecture string     `json:"architecture"`
	Modules      packagePin `json:"modules"`
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func trustedURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Host != "security.ubuntu.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.RawPath != "" || !strings.HasPrefix(u.Path, "/ubuntu/") || path.Clean(u.Path) != u.Path {
		return fmt.Errorf("untrusted Ubuntu package URL %q", raw)
	}
	return nil
}

func Client() *http.Client {
	return &http.Client{Timeout: requestTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= redirectLimit {
			return errors.New("too many Ubuntu redirects")
		}
		return trustedURL(req.URL.String())
	}}
}

func decodeLock(data []byte) (kernelLock, error) {
	var lock kernelLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return lock, err
	}
	for _, segment := range []string{lock.Suite, lock.Component, lock.Architecture} {
		if !segmentPattern.MatchString(segment) {
			return lock, errors.New("invalid kernel repository path")
		}
	}
	for _, pin := range []packagePin{lock.packagePin, lock.Modules} {
		if pin.Package == "" || pin.Version == "" || !validDigest(pin.SHA256) {
			return lock, errors.New("incomplete kernel package lock")
		}
		if err := trustedURL(pin.URL); err != nil {
			return lock, err
		}
	}
	return lock, nil
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, errors.New("kernel metadata or package exceeds size limit")
	}
	return data, nil
}

func download(client *http.Client, location string, maximum int64) ([]byte, error) {
	if err := trustedURL(location); err != nil {
		return nil, err
	}
	response, err := client.Get(location)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.Join(fmt.Errorf("Ubuntu download returned HTTP %d", response.StatusCode), response.Body.Close())
	}
	data, readErr := readBounded(response.Body, maximum)
	return data, errors.Join(readErr, response.Body.Close())
}

func digestMatches(data []byte, expected string) bool {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]) == expected
}

func signedIndex(release []byte, relative string) (string, int64, error) {
	active, digest, size := false, "", int64(0)
	for line := range strings.SplitSeq(string(release), "\n") {
		if line == "SHA256:" {
			active = true
			continue
		}
		if !strings.HasPrefix(line, " ") {
			active = false
		}
		if !active {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != fieldCount || fields[2] != relative {
			continue
		}
		parsed, err := strconv.ParseInt(fields[1], 10, 64)
		if digest != "" || !validDigest(fields[0]) || err != nil || parsed <= 0 || parsed > maxIndexBytes {
			return "", 0, errors.New("invalid or ambiguous signed index entry")
		}
		digest, size = fields[0], parsed
	}
	if digest == "" {
		return "", 0, errors.New("required index absent from signed SHA256 metadata")
	}
	return digest, size, nil
}

func packageRecords(data []byte) ([]map[string]string, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	expanded, readErr := readBounded(compressed, maxExpandedIndexBytes)
	if err := errors.Join(readErr, compressed.Close()); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(expanded))
	scanner.Buffer(nil, maxIndexLineBytes)
	var records []map[string]string
	record := make(map[string]string)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(record) != 0 {
				records = append(records, record)
			}
			record = make(map[string]string)
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ": ")
		_, duplicate := record[key]
		if !ok || key == "" || duplicate {
			return nil, errors.New("malformed or duplicate Debian package field")
		}
		record[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(record) != 0 {
		records = append(records, record)
	}
	return records, nil
}

func verifyPackageRecord(records []map[string]string, pin packagePin) error {
	found := false
	for _, record := range records {
		if record["Package"] != pin.Package || record["Version"] != pin.Version {
			continue
		}
		if found || record["SHA256"] != pin.SHA256 || ubuntuBase+record["Filename"] != pin.URL {
			return errors.New("kernel lock differs from signed metadata or matches multiple records")
		}
		found = true
	}
	if !found {
		return errors.New("locked kernel package absent from signed repository index")
	}
	return nil
}

// Run downloads only locked packages after signature, index, and package verification.
// verify must authenticate InRelease using the independently trusted Ubuntu keyring.
func Run(lockPath, output string, client *http.Client, verify func(string) error) error {
	data, err := os.ReadFile(lockPath) // #nosec G304 -- explicit operator-selected lock file.
	if err != nil {
		return err
	}
	lock, err := decodeLock(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(output, directoryMode); err != nil {
		return err
	}
	distribution := ubuntuBase + "dists/" + lock.Suite + "/"
	release, err := download(client, distribution+"InRelease", maxReleaseBytes)
	if err != nil {
		return err
	}
	releasePath := filepath.Join(output, "InRelease")
	if err := os.WriteFile(releasePath, release, fileMode); err != nil {
		return err
	}
	if err := verify(releasePath); err != nil {
		return fmt.Errorf("Ubuntu signature verification: %w", err)
	}
	relative := lock.Component + "/binary-" + lock.Architecture + "/Packages.gz"
	digest, size, err := signedIndex(release, relative)
	if err != nil {
		return err
	}
	index, err := download(client, distribution+path.Dir(relative)+"/by-hash/SHA256/"+digest, maxIndexBytes)
	if err != nil {
		return err
	}
	if int64(len(index)) != size || !digestMatches(index, digest) {
		return errors.New("repository index checksum or size mismatch")
	}
	if err := os.WriteFile(filepath.Join(output, "Packages.gz"), index, fileMode); err != nil {
		return err
	}
	records, err := packageRecords(index)
	if err != nil {
		return err
	}
	for _, artifact := range []struct {
		pin  packagePin
		name string
	}{{lock.packagePin, "kernel.deb"}, {lock.Modules, "modules.deb"}} {
		if err := verifiedDownload(client, records, artifact.pin, filepath.Join(output, artifact.name)); err != nil {
			return err
		}
	}
	return nil
}

func verifiedDownload(client *http.Client, records []map[string]string, pin packagePin, output string) error {
	if err := verifyPackageRecord(records, pin); err != nil {
		return err
	}
	archive, err := download(client, pin.URL, maxPackageBytes)
	if err != nil {
		return err
	}
	if !digestMatches(archive, pin.SHA256) {
		return errors.New("kernel package checksum mismatch")
	}
	return os.WriteFile(output, archive, fileMode)
}
