package pack

import (
	"debug/elf"
	"fmt"
	"math"
)

// Validate executable structure, including PIE/static PIE (ET_DYN), without
// claiming cross-variant interpreter or libc compatibility.
func validateExecutableELF(f *elf.File, size uint64) error {
	if f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return fmt.Errorf("expected executable ELF type ET_EXEC or ET_DYN, got %s", f.Type)
	}
	if f.Entry == 0 {
		return fmt.Errorf("executable ELF has no entry point")
	}
	entryLoaded := false
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD {
			continue
		}
		if p.Filesz > p.Memsz || p.Off > size || p.Filesz > size-p.Off || p.Memsz > math.MaxUint64-p.Vaddr {
			return fmt.Errorf("invalid loadable segment bounds")
		}
		if p.Align > 1 && (p.Align&(p.Align-1) != 0 || p.Vaddr%p.Align != p.Off%p.Align) {
			return fmt.Errorf("invalid loadable segment alignment")
		}
		if p.Flags&elf.PF_X != 0 && f.Entry >= p.Vaddr && f.Entry-p.Vaddr < p.Filesz {
			entryLoaded = true
		}
	}
	if !entryLoaded {
		return fmt.Errorf("entry point is not in a file-backed executable loadable segment")
	}
	return nil
}
