package coveragegate

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func profiles(t *testing.T, contents ...string) []string {
	t.Helper()
	root := t.TempDir()
	var paths []string
	for i, content := range contents {
		path := filepath.Join(root, fmt.Sprint(i))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		paths = append(paths, path)
	}
	return paths
}

func TestMergeProfileUnion(t *testing.T) {
	t.Parallel()
	paths := profiles(t,
		"mode: atomic\np/shared.go:1.1,2.1 3 0\np/full.go:1.1,2.1 2 4\n",
		"mode: atomic\np/shared.go:1.1,2.1 3 7\np/minimal.go:1.1,2.1 1 0\n")
	got, err := Merge(paths)
	require.NoError(t, err)
	require.Equal(t, "mode: atomic\np/full.go:1.1,2.1 2 4\np/minimal.go:1.1,2.1 1 0\np/shared.go:1.1,2.1 3 7\n", got.Text)
	require.Equal(t, uint64(5), got.Covered)
	require.Equal(t, uint64(6), got.Total)
	reversed, err := Merge([]string{paths[1], paths[0]})
	require.NoError(t, err)
	require.Equal(t, got, reversed, "profile order must not change output")
	got, err = Merge(profiles(t, "mode: atomic\np/a.go:1.1,2.1 3 0\np/a.go:1.1,2.1 3 5\n"))
	require.NoError(t, err)
	require.Equal(t, "mode: atomic\np/a.go:1.1,2.1 3 5\n", got.Text)
}

func TestComplementaryProfilesUnion(t *testing.T) {
	t.Parallel()
	paths := profiles(t,
		"mode: atomic\np/a.go:1.1,2.1 50 1\np/b.go:1.1,2.1 50 0\n",
		"mode: atomic\np/a.go:1.1,2.1 50 0\np/b.go:1.1,2.1 50 1\n",
	)
	p1, err := Merge([]string{paths[0]})
	require.NoError(t, err)
	pass1, err := MeetsThreshold(p1.Covered, p1.Total, "95")
	require.NoError(t, err)
	require.False(t, pass1, "profile 1 alone (50%) must fail 95% gate")

	p2, err := Merge([]string{paths[1]})
	require.NoError(t, err)
	pass2, err := MeetsThreshold(p2.Covered, p2.Total, "95")
	require.NoError(t, err)
	require.False(t, pass2, "profile 2 alone (50%) must fail 95% gate")

	union, err := Merge(paths)
	require.NoError(t, err)
	require.Equal(t, uint64(100), union.Covered)
	require.Equal(t, uint64(100), union.Total)
	passUnion, err := MeetsThreshold(union.Covered, union.Total, "95")
	require.NoError(t, err)
	require.True(t, passUnion, "complementary union (100%) must pass 95% gate")
}

func TestRejectMalformedProfiles(t *testing.T) {
	t.Parallel()
	const good = "mode: atomic\np/a.go:1.1,2.1 3 1\n"
	for name, bad := range map[string]string{
		"empty":                       "",
		"wrong mode":                  "mode: set\n",
		"different statements":        "mode: atomic\np/a.go:1.1,2.1 4 1\n",
		"changed layout":              "mode: atomic\np/a.go:1.1,3.1 3 1\n",
		"missing block":               good + "p/a.go:3.1,4.1 1 1\n",
		"negative count":              "mode: atomic\np/a.go:1.1,2.1 3 -1\n",
		"negative statements":         "mode: atomic\np/a.go:1.1,2.1 -3 1\n",
		"inconsistent repeated block": good + "p/a.go:1.1,2.1 4 1\n",
		"extra field":                 good + "p/b.go:1.1,2.1 1 1 trailing\n",
		"invalid location":            "mode: atomic\nbad 1 1\n",
		"blank row":                   good + "\n",
		"overflow counter":            "mode: atomic\np/a.go:1.1,2.1 3 18446744073709551616\n",
		"oversized row":               "mode: atomic\n" + strings.Repeat("x", 65536),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := Merge(profiles(t, good, bad))
			require.Error(t, err)
		})
	}
	for _, paths := range [][]string{nil, profiles(t, "mode: atomic\n"), {filepath.Join(t.TempDir(), "missing")}, {t.TempDir()}} {
		_, err := Merge(paths)
		require.Error(t, err)
	}
	_, err := render(map[string]block{"a": {statements: math.MaxUint64}, "b": {statements: 1}})
	require.ErrorContains(t, err, "overflow")
	require.ErrorIs(t, mergeProfile(&brokenReader{}, map[string]block{}, map[string]map[string]uint64{}), errRead)
}

var errRead = errors.New("read failed")

type brokenReader struct{ started bool }

func (r *brokenReader) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		return copy(p, "mode: atomic\n"), nil
	}
	return 0, errRead
}

func TestExactThreshold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, threshold string
		covered, total  uint64
		pass, invalid   bool
	}{
		{"exact 95 of 100", "95", 95, 100, true, false},
		{"949999 of 1000000 fails at 95", "95", 949999, 1000000, false, false},
		{"950001 of 1000000 passes at 95", "95", 950001, 1000000, true, false},
		{"below despite display rounding", "95", 94973, 100000, false, false},
		{"equal", "95", 95000, 100000, true, false},
		{"scientific decimal", "9.5e1", 95000, 100000, true, false},
		{"positive sign", "+95", 95000, 100000, true, false},
		{"fraction is not decimal", "95/1", 95000, 100000, false, true},
		{"decimal equal", "95.001", 95001, 100000, true, false},
		{"decimal below", "95.001", 95000, 100000, false, false},
		{"zero total", "95", 0, 0, false, false},
		{"no uint overflow", "100", math.MaxUint64, math.MaxUint64, true, false},
		{"not a number", "NaN", 1, 1, false, true},
		{"negative", "-1", 1, 1, false, true},
		{"over 100", "101", 1, 1, false, true},
		{"impossible count", "95", 2, 1, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pass, err := MeetsThreshold(tc.covered, tc.total, tc.threshold)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.pass, pass)
			}
		})
	}
}
