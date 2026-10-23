package main

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	defaultLevel       = "default"
	decoderReserve     = 64 * kib
	largeReserve       = 256 * kib
	codecReserve       = 32 * mib
	workspaceBudget    = 256 * mib
	requiredFamilies   = 3
	minimumSaving      = 0.05
	maximumModeledRise = 0.10
	readMiBPerSecond   = 500.0
	decodeMiBPerSecond = 500.0
	hashMiBPerSecond   = 500.0
	writeMiBPerSecond  = 1000.0
	copyAndWritePasses = 2
)

type baseline struct {
	Name          string            `json:"name"`
	Path          string            `json:"path,omitempty"`
	Bytes         int64             `json:"complete_bytes"`
	SHA256        string            `json:"complete_sha256,omitempty"`
	Index         *format.Index     `json:"index,omitempty"`
	MetadataBytes int64             `json:"index_and_trailer_bytes"`
	FrameSHA256   map[string]string `json:"frame_sha256,omitempty"`
	Exact         int               `json:"exact_roundtrips"`
	Error         string            `json:"error,omitempty"`
}

func baselineOptions(g group, arch, stub, output string, dictionary bool) pack.Options {
	opts := pack.DefaultOptions()
	opts.AppName, opts.TargetArch, opts.StubPath, opts.OutputPath = g.family, arch, stub, output
	opts.Compression, opts.CompressionLevel, opts.EnableDict = codec.AlgorithmZstd, defaultLevel, dictionary
	for _, v := range g.variants {
		opts.Variants[v.Level] = v.Path
	}
	return opts
}

func packBaseline(g group, arch, stub, output string, dictionary bool, inputs [][]byte, stubData []byte) baseline {
	name := "no-dictionary"
	if dictionary {
		name = "current-dictionary"
	}
	b := baseline{Name: name, Path: output}
	_, err := pack.Pack(baselineOptions(g, arch, stub, output, dictionary))
	if err != nil {
		b.Error = err.Error()
		return b
	}
	data, err := readBounded(output, maxContainer)
	if err == nil {
		err = verifyBaseline(data, g, inputs, stubData, &b)
	}
	if err != nil {
		b.Error = err.Error()
	}
	return b
}

func verifyBaseline(data []byte, g group, inputs [][]byte, stub []byte, b *baseline) error {
	idx, checks, err := pack.VerifyBinary(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(checks) != len(inputs) {
		return errors.New("baseline variant count mismatch")
	}
	if len(data) < len(stub) || !bytes.Equal(data[:len(stub)], stub) {
		return errors.New("baseline stub identity changed")
	}
	b.Bytes, b.SHA256, b.Index = int64(len(data)), digest(data), idx
	b.FrameSHA256 = make(map[string]string)
	var dictionary []byte
	if idx.DictionarySize > 0 {
		if idx.DictionaryOffset != int64(len(stub)) {
			return errors.New("baseline dictionary boundary mismatch")
		}
		dictionary = data[idx.DictionaryOffset : idx.DictionaryOffset+idx.DictionarySize]
	}
	position := int64(len(stub) + len(dictionary))
	for i, v := range idx.Variants {
		if !checks[i].Valid || v.Level != g.variants[i].Level || v.SHA256 != g.variants[i].SHA256 ||
			v.UncompressedSize != g.variants[i].Bytes {
			return errors.New("baseline differs from pinned corpus")
		}
		if v.Offset != position {
			return errors.New("baseline payload boundary mismatch")
		}
		position += v.CompressedSize
		frame := data[v.Offset : v.Offset+v.CompressedSize]
		var decoded bytes.Buffer
		if err = codec.NewZstdCodec().DecompressWithDict(&decoded, bytes.NewReader(frame), v.UncompressedSize, dictionary); err != nil {
			return err
		}
		if !bytes.Equal(decoded.Bytes(), inputs[i]) {
			return errors.New("baseline byte comparison failed")
		}
		b.Exact++
		b.FrameSHA256[v.Level] = digest(frame)
	}
	b.MetadataBytes = int64(len(data)) - position
	return nil
}

type targetCost struct {
	Level                 string  `json:"level"`
	ReadBytes             int64   `json:"selected_read_bytes"`
	DecodedBytes          int64   `json:"selected_decoded_bytes"`
	HashBytes             int64   `json:"selected_hash_bytes"`
	WorkspaceBytes        int64   `json:"buffer_and_reserve_bytes"`
	ModeledSeconds        float64 `json:"modeled_seconds"`
	BaselineSeconds       float64 `json:"baseline_modeled_seconds"`
	ModeledIncrease       float64 `json:"modeled_increase_fraction"`
	WithinScreeningBudget bool    `json:"within_screening_budget"`
}

type candidate struct {
	Method              string       `json:"method"`
	Base                string       `json:"base,omitempty"`
	Path                string       `json:"path,omitempty"`
	ContainerBytes      int64        `json:"actual_container_bytes"`
	SHA256              string       `json:"container_sha256,omitempty"`
	CompleteEstimate    int64        `json:"complete_bytes_estimate_64k_decoder"`
	OptimisticEstimate  int64        `json:"complete_bytes_zero_decoder"`
	LargeEstimate       int64        `json:"complete_bytes_estimate_256k_decoder"`
	Saving              float64      `json:"complete_saving_fraction"`
	OptimisticSaving    float64      `json:"zero_decoder_saving_fraction"`
	LargeSaving         float64      `json:"large_decoder_saving_fraction"`
	InputBytes          int64        `json:"input_bytes"`
	InventoryApplicable bool         `json:"range_inventory_applicable"`
	DistinctChunkBytes  int64        `json:"distinct_chunk_bytes"`
	SharedCoverage      int64        `json:"shared_occurrence_bytes"`
	CoreCoverage        int64        `json:"universal_occurrence_bytes"`
	SubsetCoverage      int64        `json:"subset_shared_occurrence_bytes"`
	ChunkOccurrences    int          `json:"chunk_occurrences"`
	DistinctChunks      int          `json:"distinct_chunks"`
	Header              *wireHeader  `json:"container_header,omitempty"`
	Selected            []targetCost `json:"selected_target_costs,omitempty"`
	Exact               int          `json:"exact_roundtrips"`
	SizePass            bool         `json:"costed_size_pass"`
	OptimisticSizePass  bool         `json:"zero_decoder_size_pass"`
	ScreeningPass       bool         `json:"size_and_modeled_budgets_pass"`
	Error               string       `json:"error,omitempty"`
}

func modelSeconds(read, decode, hash, copyWrite int64) float64 {
	return (float64(read)/readMiBPerSecond + float64(decode)/decodeMiBPerSecond +
		float64(hash)/hashMiBPerSecond + float64(copyWrite)/writeMiBPerSecond) / mib
}

func selectedCosts(h wireHeader, containerSize int, control baseline) []targetCost {
	framing := containerSize - h.Pool.Bytes
	for _, t := range h.Targets {
		framing -= t.Residual.Bytes + t.Path.Bytes
	}
	var result []targetCost
	for i, t := range h.Targets {
		v := control.Index.Variants[i]
		baselineRead := v.CompressedSize + control.Index.DictionarySize + control.MetadataBytes
		baselineSeconds := modelSeconds(baselineRead, v.UncompressedSize,
			v.UncompressedSize+control.Index.DictionarySize+control.MetadataBytes-format.TrailerSize, v.UncompressedSize)
		read := int64(framing + h.Pool.Bytes + t.Residual.Bytes + t.Path.Bytes)
		decoded := int64(h.Pool.RawBytes + t.Residual.RawBytes)
		if t.PathCompressed {
			decoded += int64(t.Path.RawBytes)
		}
		hashed := int64(h.Pool.RawBytes + t.Residual.RawBytes + t.Path.RawBytes + t.Bytes + framing - framePrefix - sha256Bytes)
		workspace := int64(h.Pool.RawBytes + t.Residual.RawBytes + t.Path.RawBytes + t.Bytes + codecReserve)
		// One reconstruction copy plus one output write; the baseline needs one output write.
		seconds := modelSeconds(read, decoded, hashed, int64(t.Bytes)*copyAndWritePasses)
		increase := seconds/baselineSeconds - 1
		result = append(result, targetCost{Level: t.Level, ReadBytes: read, DecodedBytes: decoded, HashBytes: hashed,
			WorkspaceBytes: workspace, ModeledSeconds: seconds, BaselineSeconds: baselineSeconds, ModeledIncrease: increase,
			WithinScreeningBudget: workspace <= workspaceBudget && increase <= maximumModeledRise})
	}
	return result
}

func evaluateModel(m model, inv inventory, stubBytes int64, control baseline, output string) candidate {
	c := candidate{Method: m.method, Base: m.base, Path: output, InventoryApplicable: m.method != methodBase, DistinctChunkBytes: inv.unique,
		SharedCoverage: inv.shared, CoreCoverage: inv.core, SubsetCoverage: inv.shared - inv.core, DistinctChunks: len(inv.records)}
	for _, path := range inv.paths {
		c.ChunkOccurrences += len(path)
	}
	for _, t := range m.targets {
		c.InputBytes += int64(len(t.original))
	}
	data, h, err := encodeModel(m, defaultLevel)
	if err == nil {
		err = roundtripModel(data, m)
	}
	if err == nil {
		err = writeNew(output, data)
	}
	if err != nil {
		c.Error = err.Error()
		return c
	}
	c.ContainerBytes, c.SHA256, c.Header, c.Exact = int64(len(data)), digest(data), &h, len(m.targets)
	c.OptimisticEstimate = stubBytes + c.ContainerBytes
	c.CompleteEstimate = c.OptimisticEstimate + decoderReserve
	c.LargeEstimate = c.OptimisticEstimate + largeReserve
	c.Saving = 1 - float64(c.CompleteEstimate)/float64(control.Bytes)
	c.OptimisticSaving = 1 - float64(c.OptimisticEstimate)/float64(control.Bytes)
	c.LargeSaving = 1 - float64(c.LargeEstimate)/float64(control.Bytes)
	c.SizePass, c.OptimisticSizePass = c.Saving >= minimumSaving, c.OptimisticSaving >= minimumSaving
	c.ScreeningPass = c.SizePass
	c.Selected = selectedCosts(h, len(data), control)
	for _, cost := range c.Selected {
		c.ScreeningPass = c.ScreeningPass && cost.WithinScreeningBudget
	}
	return c
}

func roundtripModel(data []byte, m model) error {
	for _, t := range m.targets {
		out, err := decodeTarget(data, t.level)
		if err != nil {
			return err
		}
		if !bytes.Equal(out, t.original) {
			return errors.New("exact reconstruction failed")
		}
	}
	return nil
}

type groupResult struct {
	Family     string          `json:"family"`
	Profile    string          `json:"profile"`
	Variants   []corpusVariant `json:"variants"`
	Baselines  []baseline      `json:"baselines"`
	Control    string          `json:"strongest_complete_control"`
	Candidates []candidate     `json:"candidates"`
	Error      string          `json:"error,omitempty"`
}

func screenGroup(g group, arch, stub, output string, stubData []byte) groupResult {
	r := groupResult{Family: g.family, Profile: g.profile, Variants: g.variants}
	inputs, levels, err := readGroup(g, arch)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	for _, useDict := range []bool{false, true} {
		name := "no-dictionary.fat"
		if useDict {
			name = "current-dictionary.fat"
		}
		r.Baselines = append(r.Baselines, packBaseline(g, arch, stub, filepath.Join(output, name), useDict, inputs, stubData))
	}
	control := r.Baselines[0]
	if control.Error != "" {
		r.Error = fmt.Sprintf("mandatory no-dictionary control failed: %s", control.Error)
		return r
	}
	if other := r.Baselines[1]; other.Error == "" && other.Bytes < control.Bytes {
		control = other
	}
	r.Control = control.Name
	for _, method := range []string{methodSections, methodSmall, methodLarge, methodCDC} {
		inv, inventoryErr := buildInventory(inputs, method)
		if inventoryErr != nil {
			r.Candidates = append(r.Candidates, candidate{Method: method, Error: inventoryErr.Error()})
			continue
		}
		m := sharingModel(inv, levels, inputs, method)
		r.Candidates = append(r.Candidates, evaluateModel(m, inv, int64(len(stubData)), control, filepath.Join(output, method+".mf47")))
	}
	for base := range inputs {
		m := baseModel(inputs, levels, base)
		r.Candidates = append(r.Candidates, evaluateModel(m, inventory{}, int64(len(stubData)), control,
			filepath.Join(output, methodBase+"-"+levels[base]+".mf47")))
	}
	return r
}
