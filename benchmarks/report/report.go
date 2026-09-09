// Package report verifies and renders benchmark evidence without running workloads.
package report

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	tablePadding   = 2
	fileMode       = 0o600
	dirMode        = 0o700
	maxBundleBytes = 512 * 1024 * 1024
	maxBundleFiles = 50000
	checksumName   = "SHA256SUMS"
)

type Table struct {
	Experiment      string                     `json:"experiment"`
	SourceSHA       string                     `json:"source_sha"`
	Environment     schema.EnvironmentSnapshot `json:"environment"`
	Complete        bool                       `json:"complete"`
	ReleaseEligible bool                       `json:"release_eligible"`
	Warnings        []string                   `json:"warnings"`
	Rows            []Row                      `json:"rows"`
	Comparisons     []schema.Comparison        `json:"comparisons"`
	Configurations  []schema.Configuration     `json:"configurations"`
	Artifacts       []schema.Artifact          `json:"artifacts"`
	HostTelemetry   map[string]string          `json:"host_telemetry"`
}

type Row struct {
	Trial         string             `json:"trial"`
	Configuration string             `json:"configuration"`
	Outcome       string             `json:"outcome"`
	Reason        string             `json:"reason,omitempty"`
	Metric        string             `json:"metric"`
	Measurement   schema.Measurement `json:"measurement"`
	Reference     string             `json:"reference"`
}

func Build(exp *schema.ExperimentV2) (*Table, error) {
	if err := schema.ValidateV2(exp); err != nil {
		return nil, err
	}
	table := &Table{Experiment: exp.ID, SourceSHA: exp.SourceSHA, Environment: exp.Environment,
		Complete: exp.Complete, ReleaseEligible: exp.ReleaseEligible, Warnings: exp.Warnings, Comparisons: exp.Comparisons,
		Configurations: exp.Configurations, Artifacts: exp.Artifacts, HostTelemetry: exp.HostTelemetry}
	for index, trial := range exp.Trials {
		reference := "raw.json#/trials/" + strconv.Itoa(index)
		keys := make([]string, 0, len(trial.Metrics))
		for key := range trial.Metrics {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		if len(keys) == 0 {
			table.Rows = append(table.Rows, Row{Trial: trial.ID, Configuration: trial.ConfigurationID,
				Outcome: trial.Outcome, Reason: trial.Reason, Reference: reference})
		}
		for _, key := range keys {
			table.Rows = append(table.Rows, Row{Trial: trial.ID, Configuration: trial.ConfigurationID,
				Outcome: trial.Outcome, Reason: trial.Reason, Metric: key, Measurement: trial.Metrics[key],
				Reference: reference + "/metrics/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(key)})
		}
	}
	return table, nil
}

func Render(exp *schema.ExperimentV2, format string) ([]byte, error) {
	table, err := Build(exp)
	if err != nil {
		return nil, err
	}
	if format == "json" {
		return schema.CanonicalV2(table)
	}
	if format != "markdown" && format != "terminal" {
		return nil, errors.New("format must be markdown, terminal or json")
	}
	var output bytes.Buffer
	_, _ = fmt.Fprintf(&output, "Benchmark %s\nSource: %s; complete: %t; release eligible: %t\n",
		exp.ID, exp.SourceSHA, exp.Complete, exp.ReleaseEligible)
	_, _ = fmt.Fprintf(&output, "CPU: %s; kernel: %s; Go: %s; seed: %d\n",
		exp.Environment.Host.CPU.ModelName, exp.Environment.Host.KernelRelease, exp.Environment.Process.GoVersion, exp.Seed)
	for _, warning := range exp.Warnings {
		_, _ = fmt.Fprintf(&output, "Qualification: %s\n", clean(warning))
	}
	_, _ = fmt.Fprintln(&output, "\nLatency percentiles are histogram estimates; intervals use paired trial bootstrap.")
	_, _ = fmt.Fprintln(&output, "See raw.json for controls, failures, cache/tuning state, artifacts and native histograms.")
	for _, artifact := range exp.Artifacts {
		_, _ = fmt.Fprintf(&output, "Artifact %s: SHA256 %s; Go %s; build/settings %s\n",
			clean(artifact.ID), artifact.SHA256, clean(artifact.GoVersion), clean(fmt.Sprint(artifact.Settings)))
	}
	if format == "markdown" {
		_, _ = fmt.Fprintln(&output, "\n| Configuration | Mode | Cache materialization | Tuning | ISA |\n| --- | --- | --- | --- | --- |")
		for _, cfg := range exp.Configurations {
			_, _ = fmt.Fprintf(&output, "| %s | %s | %s | %s | %s |\n", clean(cfg.ID), clean(cfg.Mode), clean(cfg.Cache),
				clean(cfg.Tuning), clean(cfg.Level))
		}
		_, _ = fmt.Fprintln(&output, "\n| Trial / config | Metric | Value / unit | Phase / source | Origin |\n| --- | --- | --- | --- | --- |")
		for _, row := range table.Rows {
			_, _ = fmt.Fprintf(&output, "| %s / %s (%s) | %s | %s | %s / %s | %s |\n", clean(row.Trial), clean(row.Configuration),
				clean(row.Outcome), clean(row.Metric), valueText(row), clean(row.Measurement.Phase), clean(row.Measurement.Source),
				clean(row.Reference))
		}
	} else {
		writer := tabwriter.NewWriter(&output, 0, 0, tablePadding, ' ', 0)
		_, _ = fmt.Fprintln(writer, "\nCONFIGURATION\tMODE\tCACHE\tTUNING\tISA")
		for _, cfg := range exp.Configurations {
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", clean(cfg.ID), clean(cfg.Mode), clean(cfg.Cache),
				clean(cfg.Tuning), clean(cfg.Level))
		}
		_, _ = fmt.Fprintln(writer, "\nTRIAL / CONFIG\tMETRIC\tVALUE / UNIT\tOUTCOME")
		for _, row := range table.Rows {
			_, _ = fmt.Fprintf(writer, "%s / %s\t%s\t%s\t%s\n", row.Trial, row.Configuration, row.Metric, valueText(row), row.Outcome)
		}
		if err := writer.Flush(); err != nil {
			return nil, err
		}
	}
	_, _ = fmt.Fprintln(&output, "\nPaired differences (candidate minus baseline):")
	for _, c := range exp.Comparisons {
		interval := "95% CI unavailable"
		if c.Status == "complete" {
			interval = fmt.Sprintf("95%% CI [%.6g, %.6g]", c.Low, c.High)
		}
		_, _ = fmt.Fprintf(&output, "%s vs %s; %s: [Derived] %.6g %s; %s; n=%d; %s; %s\n",
			clean(c.Candidate), clean(c.Baseline), clean(c.Metric), c.MedianDifference, c.Unit, interval, len(c.Pairs),
			c.Status, clean(c.Reason))
	}
	return output.Bytes(), nil
}

func clean(value string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "\t", " ", "<", "&lt;", ">", "&gt;").Replace(value)
}

func valueText(row Row) string {
	m := row.Measurement
	if m.Value == nil {
		return "unavailable: " + clean(m.Reason+" "+row.Reason)
	}
	return fmt.Sprintf("[%s] %.6g %s", m.Kind, *m.Value, clean(m.Unit))
}

func WriteBundle(exp *schema.ExperimentV2, destination string, extra map[string][]byte) (err error) {
	if err := schema.ValidateV2(exp); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("bundle destination already exists or cannot be inspected")
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, dirMode); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".evidence-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	files := make(map[string][]byte, len(extra))
	for name, data := range extra {
		if !schema.SafeReference(name) || !strings.HasPrefix(name, "trials/") {
			return errors.New("extra evidence must be trial-relative")
		}
		files[name] = data
	}
	for name, value := range map[string]any{"raw.json": exp, "environment.json": exp.Environment,
		"schedule.json": exp.Schedule, "artifacts.json": exp.Artifacts, "analysis.json": exp.Comparisons} {
		data, err := schema.CanonicalV2(value)
		if err != nil {
			return err
		}
		files[name] = data
	}
	for name, format := range map[string]string{"report.json": "json", "report.md": "markdown"} {
		data, err := Render(exp, format)
		if err != nil {
			return err
		}
		files[name] = data
	}
	if err := writeFiles(staging, files); err != nil {
		return err
	}
	if _, err := ReadBundle(staging); err != nil {
		return err
	}
	return publish(staging, destination)
}

func writeFiles(root string, files map[string][]byte) error {
	keys := make([]string, 0, len(files))
	var total int
	for key, data := range files {
		if len(data) > schema.MaxEvidenceBytes || len(data) > maxBundleBytes-total {
			return errors.New("evidence exceeds byte limit")
		}
		total += len(data)
		keys = append(keys, key)
	}
	if len(keys) > maxBundleFiles {
		return errors.New("too many evidence files")
	}
	slices.Sort(keys)
	var sums strings.Builder
	for _, name := range keys {
		if !schema.SafeReference(name) {
			return errors.New("invalid evidence file name")
		}
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), dirMode); err != nil {
			return err
		}
		if err := os.WriteFile(file, files[name], fileMode); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(&sums, "%s  %s\n", schema.Digest(files[name]), name)
	}
	return os.WriteFile(filepath.Join(root, checksumName), []byte(sums.String()), fileMode)
}

func ReadBundle(root string) (*schema.ExperimentV2, error) {
	files, err := VerifyFiles(root)
	if err != nil {
		return nil, err
	}
	var exp schema.ExperimentV2
	if err := json.Unmarshal(files["raw.json"], &exp); err != nil {
		return nil, err
	}
	if err := schema.ValidateV2(&exp); err != nil {
		return nil, err
	}
	for _, trial := range exp.Trials {
		if trial.TelemetryPath != "" {
			data, ok := files[trial.TelemetryPath]
			if !ok {
				return nil, errors.New("missing telemetry evidence")
			}
			var samples []schema.ResourceSample
			if err := json.Unmarshal(data, &samples); err != nil || len(samples) != trial.SampleCount ||
				schema.ValidateResourceSamples(samples) != nil {
				return nil, errors.New("invalid telemetry evidence or sample count")
			}
		}
		if trial.Load != nil {
			if _, ok := files[trial.Load.RawPath]; !ok {
				return nil, errors.New("missing raw load evidence")
			}
		}
	}
	return &exp, nil
}

func VerifyFiles(root string) (map[string][]byte, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("bundle must be a directory")
	}
	files := make(map[string][]byte)
	var total int
	err = filepath.WalkDir(root, func(file string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("non-regular evidence entry")
		}
		if len(files) >= maxBundleFiles {
			return errors.New("too many evidence files")
		}
		data, err := ReadBounded(file)
		if err != nil {
			return err
		}
		if len(data) > maxBundleBytes-total {
			return errors.New("bundle exceeds byte limit")
		}
		total += len(data)
		name, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	sums := files[checksumName]
	if len(sums) == 0 {
		return nil, errors.New("missing SHA256SUMS")
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(string(sums), "\n"), "\n") {
		digest, name, ok := strings.Cut(line, "  ")
		if !ok || !schema.ValidDigest(digest) || !schema.SafeReference(name) || name == checksumName || seen[name] {
			return nil, errors.New("invalid checksum manifest")
		}
		data, ok := files[name]
		if !ok || schema.Digest(data) != digest {
			return nil, fmt.Errorf("evidence digest mismatch: %s", name)
		}
		seen[name] = true
	}
	if len(seen) != len(files)-1 {
		return nil, errors.New("unlisted evidence files")
	}
	return files, nil
}

func ReadBounded(path string) ([]byte, error) {
	file, err := openEvidence(path)
	if err != nil {
		return nil, err
	}
	return readDescriptor(file)
}

func readDescriptor(file *os.File) ([]byte, error) {
	opened, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if !opened.Mode().IsRegular() || opened.Size() > schema.MaxEvidenceBytes {
		return nil, errors.Join(errors.New("opened evidence is not a bounded regular file"), file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, schema.MaxEvidenceBytes+1))
	err = errors.Join(readErr, file.Close())
	if len(data) > schema.MaxEvidenceBytes {
		err = errors.Join(err, errors.New("evidence grew beyond limit"))
	}
	return data, err
}
