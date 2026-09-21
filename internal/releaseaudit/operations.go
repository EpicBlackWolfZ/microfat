package releaseaudit

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

func dispatch(runner Runner, packed string, detected detection, output string) error {
	for _, mode := range []string{"auto", "memfd", "cache"} {
		env := map[string]string{"MICROFAT_EXEC_MODE": mode, "MICROFAT_CACHE_DIR": filepath.Join(output, "cache-"+mode)}
		repeats := 1
		if mode == "cache" {
			repeats++
		}
		for range repeats {
			var value struct {
				Arch, Variant, Origin string
				Args                  []string
			}
			if err := runJSON(runner, []string{packed, "argument with spaces", "*literal*"}, env, true, &value); err != nil {
				return err
			}
			if value.Arch != detected.Arch || value.Variant != detected.Level {
				return errors.New("wrong dispatched variant")
			}
			if !slices.Equal(value.Args, []string{"argument with spaces", "*literal*"}) {
				return errors.New("arguments changed")
			}
			if value.Origin != packed {
				return errors.New("wrong original executable hint")
			}
		}
	}
	return nil
}

type cacheEntry struct {
	Mode   os.FileMode
	Digest string
}

func snapshot(directory string) (map[string]cacheEntry, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	result := make(map[string]cacheEntry, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("non-regular cache entry: %s", entry.Name())
		}
		digest, err := Digest(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		result[entry.Name()] = cacheEntry{Mode: info.Mode(), Digest: digest}
	}
	return result, nil
}

func fullOperations(runner Runner, packed, output, arch string) error {
	if _, err := runner.Run([]string{packed, "--microfat:info"}, nil, true); err != nil {
		return err
	}
	cache := filepath.Join(output, "prewarm")
	env := map[string]string{"MICROFAT_CACHE_DIR": cache}
	if _, err := runner.Run([]string{packed, "--microfat:prewarm=all,json"}, env, true); err != nil {
		return err
	}
	before, err := snapshot(cache)
	if err != nil {
		return err
	}
	if _, err := runner.Run([]string{packed, "--microfat:prewarm=all,verify,json"}, env, true); err != nil {
		return err
	}
	after, err := snapshot(cache)
	if err != nil {
		return err
	}
	if !maps.Equal(before, after) {
		return errors.New("cache verification changed files or permissions")
	}
	native := filepath.Join(output, "native")
	if _, err := runner.Run([]string{packed, "--microfat:optimize-to=" + native}, nil, true); err != nil {
		return err
	}
	var value struct{ Arch string }
	if err := runJSON(runner, []string{native}, nil, true, &value); err != nil {
		return err
	}
	if value.Arch != arch {
		return errors.New("wrong optimized payload architecture")
	}
	return nil
}

func rejectCorruption(runner Runner, cli, packed string, index packedIndex, output string) error {
	damaged, err := os.ReadFile(packed) // #nosec G304 -- audit-generated image.
	if err != nil {
		return err
	}
	if len(index.Variants) == 0 {
		return errors.New("packed index has no variants to corrupt")
	}
	for _, variant := range index.Variants {
		if variant.Offset < 0 || variant.Offset >= int64(len(damaged)) {
			return errors.New("invalid variant offset")
		}
		damaged[variant.Offset] ^= 0xFF
	}
	corrupt := filepath.Join(output, "corrupt")
	root, err := os.OpenRoot(output)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile("corrupt", damaged, executableMode); err != nil {
		return err
	}
	if err := runJSON(runner, []string{cli, "verify", corrupt, "--json"}, nil, false, new(any)); err != nil {
		return err
	}
	for _, mode := range []string{"memfd", "cache"} {
		if _, err := runner.Run([]string{corrupt}, map[string]string{"MICROFAT_EXEC_MODE": mode,
			"MICROFAT_CACHE_DIR": filepath.Join(output, "corrupt-cache")}, false); err != nil {
			return err
		}
	}
	return nil
}
