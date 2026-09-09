package report

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

// ReadLegacy accepts original v1 envelopes and raw v1 payloads without rewriting their digest bytes.
func ReadLegacy(path string) (*schema.Experiment, bool, error) {
	data, err := ReadBounded(path)
	if err != nil {
		return nil, false, err
	}
	var envelope schema.Evidence
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, false, err
	}
	hashed := len(envelope.PayloadBytes) > 0
	if hashed {
		if err := schema.VerifyEvidence(&envelope); err != nil {
			return nil, false, err
		}
		data = envelope.PayloadBytes
	}
	var exp schema.Experiment
	if err := json.Unmarshal(data, &exp); err != nil {
		return nil, hashed, err
	}
	if err := schema.ValidateExperiment(&exp); err != nil {
		return nil, hashed, err
	}
	return &exp, hashed, nil
}

func VerifyInput(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		_, err := ReadBundle(path)
		return "Evidence verified (v2 bundle)", err
	}
	_, hashed, err := ReadLegacy(path)
	if hashed {
		return "Evidence verified (v1 envelope)", err
	}
	return "v1 structure valid; raw payload has no envelope checksum", err
}

func RenderInput(path, format string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		exp, err := ReadBundle(path)
		if err != nil {
			return nil, err
		}
		return Render(exp, format)
	}
	exp, hashed, err := ReadLegacy(path)
	if err != nil {
		return nil, err
	}
	if format == "json" {
		return schema.SerializeCanonical(exp)
	}
	if format != "terminal" && format != "markdown" {
		return nil, errors.New("format must be terminal, markdown or json")
	}
	var output bytes.Buffer
	_, _ = fmt.Fprintf(&output, "Legacy v1 benchmark %s; envelope checksum verified: %t\n", clean(exp.ID), hashed)
	_, _ = fmt.Fprintln(&output, "Only legacy recorded metrics are available; no server histogram or paired comparisons.")
	for _, scenario := range exp.Scenarios {
		_, _ = fmt.Fprintf(&output, "%s: [Derived] %.6g ops/s; raw sample count %d\n", clean(scenario.Name),
			scenario.Analysis.ThroughputOpsPerSec, scenario.Analysis.SampleCount)
		keys := make([]string, 0, len(scenario.Analysis.PercentilesNs))
		for key := range scenario.Analysis.PercentilesNs {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			_, _ = fmt.Fprintf(&output, "%s: [Derived] %.6g ns (%s)\n", clean(key), scenario.Analysis.PercentilesNs[key],
				scenario.Analysis.PercentileMethod)
		}
	}
	return output.Bytes(), nil
}
