//go:build linux

package compress

import (
	"crypto/sha256"
	"debug/elf"
	"fmt"
	"io"

	"golang.org/x/sys/unix"
)

const memfdVerifyChunkBytes = 256 * 1024

// pwriteAll writes the full slice at off on fd without relying on a shared file cursor.
func pwriteAll(fd int, data []byte, off int64) error {
	for written := 0; written < len(data); {
		n, err := unix.Pwrite(fd, data[written:], off+int64(written))
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		written += n
	}
	return nil
}

// preadAll reads len(buf) bytes from fd starting at off without altering the file cursor.
func preadAll(fd int, buf []byte, off int64) error {
	for read := 0; read < len(buf); {
		n, err := unix.Pread(fd, buf[read:], off+int64(read))
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		read += n
	}
	return nil
}

// DecodeVariantToSealedMemfd reconstructs a Format v3 variant directly into a 4-seal Linux memfd
// using parallel workers calling Pwrite, optionally unfiltering executable sections in place.
func DecodeVariantToSealedMemfd(
	arch *V3Archive,
	r io.ReaderAt,
	level string,
	workers int,
	verifyFullImageSHA256 bool,
) (int, error) {
	if arch == nil {
		return -1, ErrInvalidV3Container
	}
	idx, ok := arch.variantByLvl[level]
	if !ok {
		return -1, fmt.Errorf("%w: %s", ErrVariantNotFound, level)
	}
	v := &arch.Variants[idx]

	fd, err := unix.MemfdCreate("microfat-v3", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return -1, fmt.Errorf("memfd_create: %w", err)
	}
	closeOnErr := true
	defer func() {
		if closeOnErr {
			_ = unix.Close(fd)
		}
	}()

	if err := unix.Ftruncate(fd, int64(v.UncompressedSize)); err != nil { // #nosec G115 -- validated <= v3MaxVariantBytes
		return -1, fmt.Errorf("ftruncate memfd: %w", err)
	}

	err = decodeBlocksParallel(arch, r, v, workers, func(offset uint64, data []byte) error {
		return pwriteAll(fd, data, int64(offset)) // #nosec G115 -- validated < UncompressedSize
	})
	if err != nil {
		return -1, err
	}

	if v.Filtered && len(v.ExecRanges) > 0 {
		for _, er := range v.ExecRanges {
			secLen := er.End - er.Start
			buf := make([]byte, secLen)
			secOff := int64(er.Start) // #nosec G115 -- validated < UncompressedSize
			if err := preadAll(fd, buf, secOff); err != nil {
				return -1, fmt.Errorf("pread exec range [%d,%d): %w", er.Start, er.End, err)
			}
			ranges := []SectionRange{{Start: 0, End: secLen}}
			if err := ApplyELFFilter(buf, ranges, elf.Machine(arch.Machine), false); err != nil {
				return -1, err
			}
			if err := pwriteAll(fd, buf, secOff); err != nil {
				return -1, fmt.Errorf("pwrite exec range [%d,%d): %w", er.Start, er.End, err)
			}
		}
	}

	if verifyFullImageSHA256 {
		hasher := sha256.New()
		buf := make([]byte, memfdVerifyChunkBytes)
		total := int64(v.UncompressedSize) // #nosec G115 -- validated <= v3MaxVariantBytes
		for off := int64(0); off < total; {
			chunkLen := min(int64(len(buf)), total-off)
			if err := preadAll(fd, buf[:chunkLen], off); err != nil {
				return -1, fmt.Errorf("verify memfd pread: %w", err)
			}
			_, _ = hasher.Write(buf[:chunkLen])
			off += chunkLen
		}
		if [shaLen]byte(hasher.Sum(nil)) != v.OrigSHA256 {
			return -1, fmt.Errorf("%w: memfd full SHA-256 mismatch", ErrInvalidV3Container)
		}
	}

	const fourSeals = unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, fourSeals); err != nil {
		return -1, fmt.Errorf("f_add_seals: %w", err)
	}
	closeOnErr = false
	return fd, nil
}
