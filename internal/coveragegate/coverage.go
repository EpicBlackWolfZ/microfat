// Package coveragegate unions Go atomic coverage profiles without rounding the gate.
package coveragegate

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/big"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type block struct {
	statements uint64
	count      uint64
}

type Profile struct {
	Text    string
	Covered uint64
	Total   uint64
}

var locationPattern = regexp.MustCompile(`^(.+\.go):([1-9][0-9]*)\.([1-9][0-9]*),([1-9][0-9]*)\.([1-9][0-9]*)$`)
var thresholdPattern = regexp.MustCompile(`^[+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// Merge counts shared source blocks once, retaining the greatest execution count.
// A source file seen in multiple profiles must have an identical block layout.
func Merge(paths []string) (Profile, error) {
	blocks := make(map[string]block)
	layouts := make(map[string]map[string]uint64)
	for _, path := range paths {
		file, err := os.Open(path) // #nosec G304 -- caller-selected coverage profiles.
		if err != nil {
			return Profile{}, err
		}
		err = errors.Join(mergeProfile(file, blocks, layouts), file.Close())
		if err != nil {
			return Profile{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	if len(blocks) == 0 {
		return Profile{}, errors.New("empty coverage input")
	}
	return render(blocks)
}

func mergeProfile(reader io.Reader, blocks map[string]block, layouts map[string]map[string]uint64) error {
	scanner := bufio.NewScanner(reader)
	if !scanner.Scan() || scanner.Text() != "mode: atomic" {
		return errors.New("expected atomic coverage")
	}
	files := make(map[string]map[string]uint64)
	for scanner.Scan() {
		location, filename, value, err := parseBlock(scanner.Text())
		if err != nil {
			return err
		}
		if previous, ok := blocks[location]; ok {
			if previous.statements != value.statements {
				return fmt.Errorf("inconsistent block %s", location)
			}
			value.count = max(value.count, previous.count)
		}
		blocks[location] = value
		if files[filename] == nil {
			files[filename] = make(map[string]uint64)
		}
		files[filename][location] = value.statements
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	for filename, layout := range files {
		if previous, ok := layouts[filename]; ok && !maps.Equal(previous, layout) {
			return fmt.Errorf("incompatible source layout for %s", filename)
		}
		layouts[filename] = layout
	}
	return nil
}

func parseBlock(line string) (string, string, block, error) {
	fields := strings.Fields(line)
	const fieldCount = 3
	if len(fields) != fieldCount {
		return "", "", block{}, errors.New("invalid coverage row")
	}
	location := locationPattern.FindStringSubmatch(fields[0])
	if location == nil {
		return "", "", block{}, errors.New("invalid coverage location")
	}
	statements, stmtErr := strconv.ParseUint(fields[1], 10, 64)
	count, countErr := strconv.ParseUint(fields[2], 10, 64)
	if err := errors.Join(stmtErr, countErr); err != nil {
		return "", "", block{}, fmt.Errorf("invalid block %s: %w", fields[0], err)
	}
	return fields[0], location[1], block{statements: statements, count: count}, nil
}

func render(blocks map[string]block) (Profile, error) {
	profile := Profile{}
	var text strings.Builder
	text.WriteString("mode: atomic\n")
	for _, location := range slices.Sorted(maps.Keys(blocks)) {
		value := blocks[location]
		if value.statements > math.MaxUint64-profile.Total {
			return Profile{}, errors.New("coverage statement count overflow")
		}
		profile.Total += value.statements
		if value.count > 0 {
			profile.Covered += value.statements
		}
		_, _ = fmt.Fprintf(&text, "%s %d %d\n", location, value.statements, value.count)
	}
	profile.Text = text.String()
	return profile, nil
}

// MeetsThreshold compares exact rational values; display rounding cannot pass a gate.
func MeetsThreshold(covered, total uint64, threshold string) (bool, error) {
	const percent = 100
	minimum, ok := new(big.Rat).SetString(threshold)
	if !ok || !thresholdPattern.MatchString(threshold) || minimum.Sign() < 0 ||
		minimum.Cmp(big.NewRat(percent, 1)) > 0 || covered > total {
		return false, errors.New("invalid coverage threshold or counts")
	}
	if total == 0 {
		return false, nil
	}
	actual := new(big.Rat).SetFrac(new(big.Int).SetUint64(covered), new(big.Int).SetUint64(total))
	actual.Mul(actual, big.NewRat(percent, 1))
	return actual.Cmp(minimum) >= 0, nil
}
