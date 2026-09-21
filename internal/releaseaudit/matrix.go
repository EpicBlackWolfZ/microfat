package releaseaudit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

const matrixSize = 16
const executableMode = 0o700
const payloadSource = `package main
import ("encoding/json"; "os"; "runtime")
var variant string
func main() {
 value := map[string]any{"arch":runtime.GOARCH,"variant":variant,"args":os.Args[1:],"origin":os.Getenv("MICROFAT_ORIGINAL_EXE")}
 if err := json.NewEncoder(os.Stdout).Encode(value); err != nil { panic(err) }
}
`

type detection struct {
	Arch  string `json:"arch"`
	Level string `json:"level"`
}
type variant struct{ Level, Path string }
type matrixCase struct {
	Version        int
	Profile, Codec string
	Dictionary     bool
}
type caseResult struct {
	Case   string `json:"case"`
	Arch   string `json:"arch"`
	Status string `json:"status"`
	Digest string `json:"packed_sha256"`
}
type packedIndex struct {
	Version  int    `json:"version"`
	Arch     string `json:"arch"`
	Variants []struct {
		Offset int64 `json:"offset"`
	} `json:"variants"`
}

func matrixCases() []matrixCase {
	var result []matrixCase
	for _, version := range []int{1, 2} {
		for _, profile := range []string{"full", "minimal"} {
			for _, codec := range []string{"none", "lz4", "zstd"} {
				item := matrixCase{Version: version, Profile: profile, Codec: codec}
				result = append(result, item)
				if codec == "zstd" {
					item.Dictionary = true
					result = append(result, item)
				}
			}
		}
	}
	return result
}

func (c matrixCase) name() string {
	dictionary := 0
	if c.Dictionary {
		dictionary = 1
	}
	return fmt.Sprintf("v%d-%s-%s-dict%d", c.Version, c.Profile, c.Codec, dictionary)
}

func matrix(options Options, runner Runner, products string, progress io.Writer) error {
	cli := filepath.Join(products, cliName)
	if err := checkVersion(options, runner, cli); err != nil {
		return err
	}
	detected, variants, err := buildPayloads(options, runner, cli)
	if err != nil {
		return err
	}
	var results []caseResult
	for _, item := range matrixCases() {
		result, err := exerciseCase(options, runner, products, detected, variants, item)
		if err != nil {
			return fmt.Errorf("%s: %w", item.name(), err)
		}
		results = append(results, result)
		if err := writeJSON(filepath.Join(options.Output, "results.json"), results); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(progress, "%s: %s passed\n", options.Arch, item.name()); err != nil {
			return err
		}
	}
	if len(results) != matrixSize {
		return errors.New("incomplete native matrix")
	}
	return nil
}

func checkVersion(options Options, runner Runner, cli string) error {
	version := regexp.MustCompile(`(^|[^\w.+-])` + regexp.QuoteMeta(options.Version) + `([^\w.+-]|$)`)
	for _, mode := range []string{"auto", "memfd", "cache"} {
		output, err := runner.Run([]string{cli, "--version"}, map[string]string{
			"MICROFAT_EXEC_MODE": mode, "MICROFAT_CACHE_DIR": filepath.Join(options.Output, "cli-cache-"+mode)}, true)
		if err != nil {
			return err
		}
		if !version.MatchString(output) {
			return errors.New("wrong release version")
		}
	}
	return nil
}

func buildPayloads(options Options, runner Runner, cli string) (detection, []variant, error) {
	var detected detection
	if err := runJSON(runner, []string{cli, "detect", "--json"}, nil, true, &detected); err != nil {
		return detected, nil, err
	}
	if detected.Arch != options.Arch || microarch.Rank(options.Arch, detected.Level) < 0 ||
		microarch.Normalize(detected.Level) != detected.Level {
		return detected, nil, errors.New("invalid downloaded CLI architecture or level")
	}
	baseline, levelKey := "v1", "GOAMD64"
	if options.Arch == "arm64" {
		baseline, levelKey = "v8.0", "GOARM64"
	}
	source := filepath.Join(options.Output, "payload.go")
	if err := os.WriteFile(source, []byte(payloadSource), fileMode); err != nil {
		return detected, nil, err
	}
	if _, err := runner.Run([]string{"gofmt", "-w", source}, nil, true); err != nil {
		return detected, nil, err
	}
	var variants []variant
	for _, level := range slices.Compact([]string{baseline, detected.Level}) {
		target := filepath.Join(options.Output, "payload-"+level)
		env := map[string]string{"GOOS": "linux", "GOARCH": options.Arch, "CGO_ENABLED": "0", levelKey: level}
		_, err := runner.Run([]string{"go", "build", "-buildvcs=false", "-ldflags=-s -w -X main.variant=" + level,
			"-o", target, source}, env, true)
		if err != nil {
			return detected, nil, err
		}
		variants = append(variants, variant{Level: level, Path: target})
	}
	return detected, variants, nil
}

func exerciseCase(
	options Options, runner Runner, products string, detected detection, variants []variant, item matrixCase,
) (caseResult, error) {
	output := filepath.Join(options.Output, item.name())
	result := caseResult{Case: item.name(), Arch: options.Arch, Status: "pass"}
	if err := os.Mkdir(output, directoryMode); err != nil {
		return result, err
	}
	packed, cli := filepath.Join(output, "packed"), filepath.Join(products, cliName)
	if err := packImage(options, runner, products, variants, item, packed); err != nil {
		return result, err
	}
	var index packedIndex
	if err := runJSON(runner, []string{cli, "inspect", packed, "--json"}, nil, true, &index); err != nil {
		return result, err
	}
	if index.Version != item.Version || index.Arch != options.Arch {
		return result, errors.New("wrong packed format")
	}
	if err := runJSON(runner, []string{cli, "verify", packed, "--json"}, nil, true, new(any)); err != nil {
		return result, err
	}
	if err := dispatch(runner, packed, detected, output); err != nil {
		return result, err
	}
	if item.Profile == "full" {
		if err := fullOperations(runner, packed, output, options.Arch); err != nil {
			return result, err
		}
	} else if _, err := runner.Run([]string{packed, "--microfat:help"}, nil, false); err != nil {
		return result, err
	}
	if err := rejectCorruption(runner, cli, packed, index, output); err != nil {
		return result, err
	}
	var err error
	result.Digest, err = Digest(packed)
	return result, err
}

func checkStub(packed, stub string) error {
	prefix, err := os.ReadFile(stub) // #nosec G304 -- authenticated release product.
	if err != nil {
		return err
	}
	file, err := os.Open(packed) // #nosec G304 -- audit-generated packed image.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	actual := make([]byte, len(prefix))
	if _, err := io.ReadFull(file, actual); err != nil {
		return err
	}
	if len(prefix) == 0 || !bytes.Equal(prefix, actual) {
		return errors.New("packed image did not use the downloaded stub")
	}
	return nil
}

func runJSON(runner Runner, args []string, env map[string]string, success bool, value any) error {
	output, err := runner.Run(args, env, success)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(output), value)
}

func writeJSON(filename string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, append(data, '\n'), fileMode)
}

func packImage(options Options, runner Runner, products string, variants []variant, item matrixCase, packed string) error {
	stub := filepath.Join(products, fullStub)
	if item.Profile == "minimal" {
		stub = filepath.Join(products, minimalStub)
	}
	arguments := []string{filepath.Join(products, cliName), "pack", "--arch", options.Arch, "--format-version", strconv.Itoa(item.Version),
		"--compression", item.Codec, "-o", packed}
	if item.Version != 2 || item.Profile != "full" || item.Codec != "none" {
		arguments = append(arguments, "--stub", stub)
	}
	if item.Dictionary {
		arguments = append(arguments, "--dict")
	}
	for _, variant := range variants {
		arguments = append(arguments, "-v", variant.Level+"="+variant.Path)
	}
	if _, err := runner.Run(arguments, nil, true); err != nil {
		return err
	}
	if err := checkStub(packed, stub); err != nil {
		return err
	}
	return nil
}
