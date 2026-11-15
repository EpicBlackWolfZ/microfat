package kernelsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const pinnedVersion = "5.15.0-99.111"

func guestTestPin(kind, abi, version string) packagePin {
	name := fmt.Sprintf("linux-%s-5.15.0-%s-generic", kind, abi)
	return packagePin{
		Package: name,
		Version: version,
		URL:     fmt.Sprintf("%spool/main/l/linux/%s_%s_amd64.deb", ubuntuBase, name, version),
		SHA256:  strings.Repeat("a", 64),
	}
}

func guestRecord(pin packagePin) string {
	return fmt.Sprintf("Package: %s\nVersion: %s\nArchitecture: amd64\nFilename: %s\nSHA256: %s\n\n",
		pin.Package, pin.Version, strings.TrimPrefix(pin.URL, ubuntuBase), pin.SHA256)
}

func guestPair(abi, version string) string {
	return guestRecord(guestTestPin("image", abi, version)) + guestRecord(guestTestPin("modules", abi, version))
}

func reviewFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.lock.packagePin = guestTestPin("image", "99", pinnedVersion)
	f.lock.Modules = guestTestPin("modules", "99", pinnedVersion)
	return f
}

func TestCheckGuest(t *testing.T) {
	t.Parallel()
	const updated = "5.15.0-100.120"
	records := guestPair("99", pinnedVersion)
	latest := guestPair("100", updated)
	for _, tc := range []struct {
		name       string
		records    string
		wantErr    string
		wantUpdate bool
	}{
		{name: "current", records: records},
		{name: "ignores partial builds and other flavours", records: records + guestRecord(guestTestPin("image", "107", "5.15.0-107.124")) +
			strings.ReplaceAll(guestPair("108", "5.15.0-108.125"), "-generic", "-aws") +
			strings.ReplaceAll(guestPair("109", "5.15.0-109.126"), "Architecture: amd64", "Architecture: arm64") +
			guestPair("110", "6.8.0-110.127") +
			guestRecord(guestTestPin("image", "111", "5.15.0-111.128")) +
			guestRecord(guestTestPin("modules", "111", "5.15.0-111.129"))},
		{name: "latest complete numeric ABI pair", records: latest + records + guestPair("98", "5.15.0-98.110"),
			wantErr: "new signed guest build " + updated, wantUpdate: true},
		{name: "latest upload of same ABI", records: records + guestPair("99", "5.15.0-99.112"),
			wantErr: "new signed guest build 5.15.0-99.112", wantUpdate: true},
		{name: "update survives missing old package", records: latest,
			wantErr: "new signed guest build " + updated, wantUpdate: true},
		{name: "missing paired packages", records: guestRecord(guestTestPin("image", "100", updated)), wantErr: "no matching"},
		{name: "older metadata", records: guestPair("98", "5.15.0-98.110"), wantErr: "older than the locked"},
		{name: "duplicate", records: records + latest + latest, wantErr: "ambiguous signed record"},
		{name: "bad hash", records: records + strings.ReplaceAll(latest, strings.Repeat("a", 64), "bad"), wantErr: "invalid"},
		{name: "different hash for same pin", records: strings.ReplaceAll(records, strings.Repeat("a", 64), strings.Repeat("b", 64)),
			wantErr: "differs from signed metadata"},
		{name: "unexpected repository path", records: records + strings.ReplaceAll(latest, "pool/main/", "pool/universe/"),
			wantErr: "invalid"},
		{name: "invalid package URL", records: records + strings.ReplaceAll(latest, "pool/main/", "pool/../"),
			wantErr: "untrusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := reviewFixture(t)
			f.setIndex(compress(t, tc.records))
			output := t.TempDir()
			var report strings.Builder
			err := Check(f.writeLock(t), output, f.client, f.verify, &report)
			if tc.wantErr == "" {
				require.NoError(t, err)
				require.Contains(t, report.String(), "is current at "+pinnedVersion)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
				require.Empty(t, report.String())
			}
			require.Equal(t, []string{releaseURL, f.indexURL}, f.requests, "monitoring must not download or execute packages")
			candidate := filepath.Join(output, "kernel.lock.candidate.json")
			if !tc.wantUpdate {
				require.NoFileExists(t, candidate)
				return
			}
			data, err := os.ReadFile(candidate)
			require.NoError(t, err)
			var lock kernelLock
			require.NoError(t, json.Unmarshal(data, &lock))
			require.Equal(t, lock.Version, lock.Modules.Version)
			require.Contains(t, tc.wantErr, lock.Version)
			require.Equal(t, "jammy-security", lock.Suite)
			require.Equal(t, strings.Repeat("a", 64), lock.SHA256)
			require.Equal(t, strings.Repeat("a", 64), lock.Modules.SHA256)
		})
	}
}

func TestCheckRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	f := reviewFixture(t)
	output := t.TempDir()
	err := Check(filepath.Join(output, "missing-lock"), output, f.client, f.verify, io.Discard)
	require.Error(t, err)
	for name, edit := range map[string]func(*kernelLock){
		"other suite":         func(lock *kernelLock) { lock.Suite = "jammy-updates" },
		"other architecture":  func(lock *kernelLock) { lock.Architecture = "arm64" },
		"other version":       func(lock *kernelLock) { lock.Version = "6.8.0-1.1" },
		"overflowing version": func(lock *kernelLock) { lock.Version = "5.15.0-999999999999999999999.1" },
		"mismatched modules":  func(lock *kernelLock) { lock.Modules.Version = "5.15.0-98.110" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := reviewFixture(t)
			edit(&f.lock)
			err := Check(f.writeLock(t), output, f.client, f.verify, io.Discard)
			require.Error(t, err)
			require.Empty(t, f.requests)
		})
	}
}

func TestCheckRequiresAuthenticatedRecords(t *testing.T) {
	t.Parallel()
	f := reviewFixture(t)
	f.setIndex(compress(t, guestPair("100", "5.15.0-100.120")))
	output := t.TempDir()
	lock := f.writeLock(t)
	err := Check(lock, output, f.client, func(string) ([]byte, error) { return nil, errors.New("invalid signature") }, io.Discard)
	require.ErrorContains(t, err, "signature verification")
	require.Equal(t, []string{releaseURL}, f.requests)
	err = Check(lock, output, f.client, func(string) ([]byte, error) { return []byte("SHA256:\n"), nil }, io.Discard)
	require.ErrorContains(t, err, "required index absent", "use only the authenticated plaintext")
	require.Equal(t, []string{releaseURL, releaseURL}, f.requests)

	require.NoError(t, os.Mkdir(filepath.Join(output, "kernel.lock.candidate.json"), directoryMode))
	err = Check(lock, output, f.client, f.verify, io.Discard)
	require.ErrorContains(t, err, "is a directory")
}
