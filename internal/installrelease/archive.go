package installrelease

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/install"
)

const maxExtractedBytes int64 = 500 * 1024 * 1024
const maxArchiveEntries = 1024
const executableMode = 0o755

func extract(archive, destination, arch string) (map[string]install.File, error) {
	input, err := os.Open(archive) // #nosec G304 -- selected authenticated archive in private staging.
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Close() }()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		return nil, err
	}
	defer func() { _ = compressed.Close() }()
	bounded := &io.LimitedReader{R: compressed, N: maxExtractedBytes + 1}
	reader := tar.NewReader(bounded)
	root, err := os.OpenRoot(destination)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	seen := make(map[string]bool)
	files := make(map[string]install.File)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name, err := validateEntry(header, seen)
		if err != nil {
			return nil, err
		}
		seen[name] = true
		if slices.Contains(install.Products(), name) {
			file, err := extractProduct(root, reader, header, name)
			if err != nil {
				return nil, err
			}
			files[name] = file
		}
	}
	if err := drainPadding(bounded); err != nil {
		return nil, err
	}
	if len(files) != len(install.Products()) {
		return nil, errors.New("archive does not contain the complete executable triad")
	}
	for _, name := range install.Products() {
		if err := validateProduct(root, name, arch); err != nil {
			return nil, fmt.Errorf("invalid %s: %w", name, err)
		}
	}
	return files, nil
}

func validateEntry(header *tar.Header, seen map[string]bool) (string, error) {
	name := path.Clean(header.Name)
	if header.Typeflag != tar.TypeReg || path.IsAbs(header.Name) || strings.ContainsAny(header.Name, "\\\x00") ||
		slices.Contains(strings.Split(header.Name, "/"), "..") || name == "." || seen[name] {
		return "", errors.New("unsafe or duplicate archive entry")
	}
	if len(seen) >= maxArchiveEntries || header.Size < 0 || header.Size > install.MaxFileBytes {
		return "", errors.New("archive exceeds entry limits")
	}
	return name, nil
}

func extractProduct(root *os.Root, reader io.Reader, header *tar.Header, name string) (install.File, error) {
	var result install.File
	if header.Size <= 0 || header.Mode&0o111 == 0 {
		return result, errors.New("archive product must be nonempty and executable")
	}
	output, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return result, err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(reader, header.Size+1))
	if copyErr == nil && n != header.Size {
		copyErr = errors.New("truncated archive product")
	}
	if err := errors.Join(copyErr, output.Chmod(executableMode), output.Close()); err != nil {
		return result, err
	}
	return install.File{Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func drainPadding(reader *io.LimitedReader) error {
	const block = 4096
	buffer := make([]byte, block)
	for {
		n, err := reader.Read(buffer)
		for _, value := range buffer[:n] {
			if value != 0 {
				return errors.New("unexpected data after tar archive")
			}
		}
		if reader.N == 0 {
			return errors.New("archive exceeds decompressed size limit")
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func validateProduct(root *os.Root, name, arch string) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	const headerSize = 64
	var header [headerSize]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return err
	}
	machine := elf.EM_X86_64
	if arch == "arm64" {
		machine = elf.EM_AARCH64
	}
	const typeOffset, machineOffset, versionOffset, sizeOffset = 16, 18, 20, 52
	typeValue := elf.Type(binary.LittleEndian.Uint16(header[typeOffset:]))
	if string(header[:4]) != elf.ELFMAG || header[elf.EI_CLASS] != byte(elf.ELFCLASS64) ||
		header[elf.EI_DATA] != byte(elf.ELFDATA2LSB) || header[elf.EI_VERSION] != byte(elf.EV_CURRENT) ||
		binary.LittleEndian.Uint32(header[versionOffset:]) != uint32(elf.EV_CURRENT) ||
		binary.LittleEndian.Uint16(header[sizeOffset:]) != headerSize ||
		elf.Machine(binary.LittleEndian.Uint16(header[machineOffset:])) != machine ||
		(typeValue != elf.ET_EXEC && typeValue != elf.ET_DYN) {
		return errors.New("ELF executable architecture does not match selected release")
	}
	if name != "microfat" {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	index, err := format.ReadTrailerAndIndex(file, info.Size())
	if err != nil {
		return err
	}
	if index.TargetOS != "linux" || index.TargetArch != arch {
		return errors.New("fat CLI target does not match selected release")
	}
	return nil
}

// Staging creates private acquisition space without relying on an executable cache.
func Staging(parent string) (string, error) {
	if parent != "" && !filepath.IsAbs(parent) {
		return "", errors.New("staging directory must be absolute")
	}
	return os.MkdirTemp(parent, "microfat-install-*")
}
