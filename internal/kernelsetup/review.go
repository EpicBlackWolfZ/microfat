package kernelsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	guestSuite        = "jammy-security"
	guestComponent    = "main"
	guestArchitecture = "amd64"
	imagePrefix       = "linux-image-"
	modulesPrefix     = "linux-modules-"
)

var guestVersionPattern = regexp.MustCompile(`^5\.15\.0-([1-9][0-9]*)\.([1-9][0-9]*)$`)

type guestVersion struct {
	abi    int
	upload int
}

func parseGuestVersion(value string) (guestVersion, error) {
	fields := guestVersionPattern.FindStringSubmatch(value)
	if fields == nil {
		return guestVersion{}, fmt.Errorf("expected a Jammy 5.15 generic guest version, got %q", value)
	}
	const abiGroup, uploadGroup = 1, 2
	abi, abiErr := strconv.Atoi(fields[abiGroup])
	upload, uploadErr := strconv.Atoi(fields[uploadGroup])
	return guestVersion{abi: abi, upload: upload}, errors.Join(abiErr, uploadErr)
}

func (v guestVersion) packageName(prefix string) string {
	return fmt.Sprintf("%s5.15.0-%d-generic", prefix, v.abi)
}

func (v guestVersion) newerThan(other guestVersion) bool {
	return v.abi > other.abi || (v.abi == other.abi && v.upload > other.upload)
}

func guestVersionFromLock(lock kernelLock) (guestVersion, error) {
	if lock.Suite != guestSuite || lock.Component != guestComponent || lock.Architecture != guestArchitecture {
		return guestVersion{}, errors.New("guest updates require the Jammy security main amd64 index")
	}
	version, err := parseGuestVersion(lock.Version)
	if err != nil {
		return guestVersion{}, err
	}
	if lock.Package != version.packageName(imagePrefix) || lock.Modules.Package != version.packageName(modulesPrefix) ||
		lock.Modules.Version != lock.Version {
		return guestVersion{}, errors.New("the guest lock must contain a matching image and modules pair")
	}
	return version, nil
}

func latestGuest(lock kernelLock, records []map[string]string) (kernelLock, guestVersion, error) {
	images := make(map[guestVersion]packagePin)
	modules := make(map[guestVersion]packagePin)
	for _, record := range records {
		name := record["Package"]
		if record["Architecture"] != lock.Architecture || (!strings.HasPrefix(name, imagePrefix+"5.15.0-") &&
			!strings.HasPrefix(name, modulesPrefix+"5.15.0-")) {
			continue
		}
		version, err := parseGuestVersion(record["Version"])
		if err != nil {
			continue
		}
		var target map[guestVersion]packagePin
		switch name {
		case version.packageName(imagePrefix):
			target = images
		case version.packageName(modulesPrefix):
			target = modules
		default:
			continue
		}
		pin := packagePin{Package: name, Version: record["Version"], URL: ubuntuBase + record["Filename"], SHA256: record["SHA256"]}
		if err := trustedURL(pin.URL); err != nil {
			return kernelLock{}, guestVersion{}, err
		}
		if _, duplicate := target[version]; duplicate || !validDigest(pin.SHA256) || !strings.HasPrefix(record["Filename"], "pool/main/") {
			return kernelLock{}, guestVersion{}, fmt.Errorf("invalid or ambiguous signed record for %s %s", pin.Package, pin.Version)
		}
		target[version] = pin
	}

	var latest guestVersion
	for version := range images {
		if _, paired := modules[version]; paired && version.newerThan(latest) {
			latest = version
		}
	}
	if latest == (guestVersion{}) {
		return kernelLock{}, latest, errors.New("no matching image and modules pair in the signed Jammy security index")
	}
	lock.packagePin, lock.Modules = images[latest], modules[latest]
	return lock, latest, nil
}

// Check authenticates the repository metadata and reports the current guest version when the lock matches it.
// If a newer matched pair is available, Check writes a proposed lock in output and returns a review error.
func Check(lockPath, output string, client *http.Client, verify func(string) ([]byte, error), report io.Writer) error {
	lock, err := loadLock(lockPath)
	if err != nil {
		return err
	}
	pinned, err := guestVersionFromLock(lock)
	if err != nil {
		return err
	}
	records, err := signedRecords(lock, output, client, verify)
	if err != nil {
		return err
	}
	candidate, latest, err := latestGuest(lock, records)
	if err != nil {
		return err
	}
	if latest == pinned {
		if candidate != lock {
			return errors.New("guest lock differs from signed metadata for the current version")
		}
		_, err := fmt.Fprintf(report, "Jammy amd64 cgroup-v1 guest is current at %s (verified Ubuntu security metadata).\n", lock.Version)
		return err
	}
	if pinned.newerThan(latest) {
		return fmt.Errorf("latest signed guest build %s is older than the locked %s", candidate.Version, lock.Version)
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	filename := filepath.Join(output, "kernel.lock.candidate.json")
	if err := os.WriteFile(filename, append(data, '\n'), fileMode); err != nil {
		return err
	}
	return fmt.Errorf("new signed guest build %s available (locked %s); review %s", candidate.Version, lock.Version, filename)
}
