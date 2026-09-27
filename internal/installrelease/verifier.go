package installrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/install"
)

const verifierTimeout = time.Minute
const maxVerifierOutput = 64 * 1024

var digestSyntax = regexp.MustCompile(`^[0-9a-f]{64}$`)

func VerifyDigest(path, expected string, limit int64) error {
	if !digestSyntax.MatchString(expected) {
		return errors.New("invalid SHA-256 trust pin")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("digest input must be a bounded regular file")
	}
	file, err := os.Open(path) // #nosec G304 -- explicit authenticated staging/verifier path, regular file checked.
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, readErr := io.Copy(hash, io.LimitReader(file, limit+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return err
	}
	if n > limit || hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("SHA-256 mismatch")
	}
	return nil
}

// Cosign always checks its independent verifier pin before executing the verifier.
// Its fields are trust inputs, never values learned from the product's unsigned metadata.
type Cosign struct{ Path, SHA256 string }

func (v Cosign) Verify(ctx context.Context, version, checksums, bundle string) error {
	version, err := ParseVersion(version)
	if err != nil {
		return err
	}
	if err := install.ValidateVerifierExecutable(v.Path); err != nil {
		return fmt.Errorf("unsafe verifier: %w", err)
	}
	if err := VerifyDigest(v.Path, v.SHA256, maxDownloadBytes); err != nil {
		return fmt.Errorf("untrusted verifier: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, verifierTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, v.Path, "verify-blob", "--bundle", bundle, "--certificate-identity", Identity(version),
		"--certificate-oidc-issuer", Issuer, checksums) // #nosec G204 -- verifier bytes independently pinned before execution.
	command.WaitDelay = time.Second
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "COSIGN_") && !strings.HasPrefix(entry, "SIGSTORE_") {
			command.Env = append(command.Env, entry)
		}
	}
	output := &boundedOutput{}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return fmt.Errorf("release signature verification failed: %w: %s", err, output.String())
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if output.truncated {
		return errors.New("release signature verification output exceeded limit")
	}
	return nil
}

type boundedOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := maxVerifierOutput - len(b.data)
	b.data = append(b.data, data[:min(len(data), remaining)]...)
	b.truncated = b.truncated || len(data) > remaining
	return len(data), nil
}

func (b *boundedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }
