package main

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

const (
	sampleBytes    = 4 * 1024
	samplesPerFile = 32
	historyDivisor = 4
)

type sample struct {
	data    []byte
	variant int
}

type candidate struct {
	policy  string
	size    int
	samples []sample
}

func validateELF(data []byte, arch string) error {
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	want := elf.EM_X86_64
	if arch == microarch.ArchARM64 {
		want = elf.EM_AARCH64
	} else if arch != microarch.ArchAMD64 {
		return errors.New("only amd64 and arm64 ELF corpora are supported")
	}
	if f.Machine != want || f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB {
		return fmt.Errorf("ELF must be little-endian 64-bit %s", arch)
	}
	return nil
}

func uniformSamples(inputs []input) []sample {
	var out []sample
	for variant, item := range inputs {
		step := max(len(item.data)/samplesPerFile, sampleBytes)
		count := 0
		for offset := 0; offset < len(item.data) && count < samplesPerFile; offset += step {
			out = append(out, sample{data: item.data[offset:min(offset+sampleBytes, len(item.data))], variant: variant})
			count++
		}
	}
	return out
}

// sectionSamples splits the same per-file count across ELF PROGBITS sections.
// Round-robin sections prevent large .text sections from exhausting every slot.
func sectionSamples(inputs []input) []sample {
	var out []sample
	for variant, item := range inputs {
		f, err := elf.NewFile(bytes.NewReader(item.data))
		if err != nil {
			continue // loadCorpus rejects malformed ELF before candidate generation.
		}
		var sections []*elf.Section
		for _, section := range f.Sections {
			if section.Type == elf.SHT_PROGBITS && section.Size >= sampleBytes &&
				section.Size <= maxInputBytes && section.Offset <= maxInputBytes &&
				section.Offset < uint64(len(item.data)) && section.Size <= uint64(len(item.data))-section.Offset {
				sections = append(sections, section)
			}
		}
		for round := 0; round < samplesPerFile && len(sections) > 0; round++ {
			section := sections[round%len(sections)]
			slots := (samplesPerFile + len(sections) - 1) / len(sections)
			position := round / len(sections)
			// Section size and offset were bounded to 64 MiB before entering sections.
			span := int(section.Size) - sampleBytes // #nosec G115 -- Explicit 64 MiB bound above fits every supported int.
			start := int(section.Offset)            // #nosec G115 -- Explicit 64 MiB bound above fits every supported int.
			offset := start + position*max(span/slots, 1)
			offset = min(offset, start+span)
			end := offset + sampleBytes
			out = append(out, sample{data: item.data[offset:end], variant: variant})
		}
		_ = f.Close()
	}
	return out
}

func candidates(inputs []input, sizes []int) []candidate {
	uniform := uniformSamples(inputs)
	section := sectionSamples(inputs)
	out := []candidate{{policy: "none"}}
	for _, size := range sizes {
		for _, policy := range []string{"current", "balanced", "deduplicated", "frequency", "core-accessory", "section"} {
			samples := uniform
			if policy == "section" {
				samples = section
			}
			out = append(out, candidate{policy: policy, size: size, samples: samples})
		}
	}
	return out
}

func balanced(samples []sample) []sample {
	result := slices.Clone(samples)
	// Stable sort by sample ordinal within variant, then by variant.
	positions := make(map[int]int)
	type indexed struct {
		sample
		position int
	}
	ordered := make([]indexed, 0, len(samples))
	for _, item := range samples {
		ordered = append(ordered, indexed{sample: item, position: positions[item.variant]})
		positions[item.variant]++
	}
	slices.SortStableFunc(ordered, func(a, b indexed) int {
		if a.position != b.position {
			return a.position - b.position
		}
		return a.variant - b.variant
	})
	for i := range ordered {
		result[i] = ordered[i].sample
	}
	return result
}

type motif struct {
	sample
	variants map[int]bool
	count    int
}

func motifs(samples []sample) []motif {
	var out []motif
	byDigest := make(map[string][]int)
	for _, item := range samples {
		key := digest(item.data)
		found := false
		for _, index := range byDigest[key] {
			if bytes.Equal(out[index].data, item.data) {
				out[index].variants[item.variant] = true
				out[index].count++
				found = true
				break
			}
		}
		if !found {
			byDigest[key] = append(byDigest[key], len(out))
			out = append(out, motif{sample: item, variants: map[int]bool{item.variant: true}, count: 1})
		}
	}
	return out
}

func selectedHistory(c candidate) ([]byte, int) {
	total := 0
	for _, item := range c.samples {
		total += len(item.data)
	}
	limit := max(min(c.size, total/historyDivisor), 8)
	ordered := c.samples
	if c.policy == "balanced" || c.policy == "section" {
		ordered = balanced(ordered)
	}
	if c.policy == "deduplicated" || c.policy == "frequency" || c.policy == "core-accessory" {
		unique := motifs(balanced(c.samples))
		if c.policy != "deduplicated" {
			slices.SortStableFunc(unique, func(a, b motif) int {
				if len(a.variants) != len(b.variants) {
					return len(b.variants) - len(a.variants)
				}
				return b.count - a.count
			})
		}
		ordered = make([]sample, 0, len(unique))
		if c.policy == "core-accessory" {
			unique = interleaveCoreAccessory(unique, c.samples)
		}
		for _, item := range unique {
			ordered = append(ordered, item.sample)
		}
	}
	var history []byte
	for _, item := range ordered {
		remaining := limit - len(history)
		if remaining <= 0 {
			break
		}
		history = append(history, item.data[:min(remaining, len(item.data))]...)
	}
	return history, total
}

func interleaveCoreAccessory(unique []motif, samples []sample) []motif {
	variants := make(map[int]bool)
	for _, item := range samples {
		variants[item.variant] = true
	}
	var core, accessory []motif
	for _, item := range unique {
		if len(item.variants) == len(variants) {
			core = append(core, item)
		} else {
			accessory = append(accessory, item)
		}
	}
	var out []motif
	for i := 0; i < max(len(core), len(accessory)); i++ {
		if i < len(core) {
			out = append(out, core[i])
		}
		if i < len(accessory) {
			out = append(out, accessory[i])
		}
	}
	return out
}

func candidateID(c candidate) string {
	if c.policy == "none" {
		return c.policy
	}
	return strings.Join([]string{c.policy, fmt.Sprint(c.size)}, "-")
}
