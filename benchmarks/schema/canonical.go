package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"time"
)

// Evidence wraps canonical benchmark experiment data with a cryptographic SHA-256 digest.
type Evidence struct {
	SchemaVersion string      `json:"schema_version"`
	DigestSHA256  string      `json:"digest_sha256"`
	PayloadBytes  []byte      `json:"payload_bytes"`
	Payload       *Experiment `json:"payload,omitempty"`
	GeneratedAt   string      `json:"generated_at"`
}

// SerializeCanonical serializes an Experiment into deterministic, formatted UTF-8 JSON bytes.
func SerializeCanonical(exp *Experiment) ([]byte, error) {
	if exp == nil {
		return nil, ErrNilExperiment
	}
	canonicalBytes, err := json.Marshal(exp, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return nil, fmt.Errorf("canonical serialization failed: %w", err)
	}
	return canonicalBytes, nil
}

// BuildEvidence validates the experiment, produces the canonical JSON payload,
// and computes the cryptographic SHA-256 digest.
func BuildEvidence(exp *Experiment) (*Evidence, error) {
	if err := ValidateExperiment(exp); err != nil {
		return nil, fmt.Errorf("experiment validation failed: %w", err)
	}

	canonicalBytes, err := SerializeCanonical(exp)
	if err != nil {
		return nil, err
	}

	h := sha256.Sum256(canonicalBytes)
	digest := hex.EncodeToString(h[:])

	return &Evidence{
		SchemaVersion: SchemaVersion,
		DigestSHA256:  digest,
		PayloadBytes:  canonicalBytes,
		Payload:       exp,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

// VerifyEvidence validates the cryptographic digest and schema version of an Evidence envelope.
func VerifyEvidence(ev *Evidence) error {
	if ev == nil {
		return errors.New("evidence cannot be nil")
	}
	if ev.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: expected %q, got %q", ErrInvalidSchemaVersion, SchemaVersion, ev.SchemaVersion)
	}

	payloadToVerify := ev.PayloadBytes
	switch {
	case len(payloadToVerify) == 0 && ev.Payload != nil:
		canonical, err := SerializeCanonical(ev.Payload)
		if err != nil {
			return fmt.Errorf("canonical serialization for verification failed: %w", err)
		}
		payloadToVerify = canonical
	case len(payloadToVerify) > 0 && ev.Payload != nil:
		canonical, err := SerializeCanonical(ev.Payload)
		if err != nil {
			return fmt.Errorf("canonical serialization for verification failed: %w", err)
		}
		if !bytes.Equal(canonical, payloadToVerify) {
			return errors.New("evidence payload does not match payload bytes")
		}
	case len(payloadToVerify) == 0 && ev.Payload == nil:
		return errors.New("evidence payload and payload bytes cannot both be empty")
	}

	h := sha256.Sum256(payloadToVerify)
	computed := hex.EncodeToString(h[:])
	if computed != ev.DigestSHA256 {
		return fmt.Errorf("evidence digest mismatch: expected %s, computed %s", ev.DigestSHA256, computed)
	}
	return nil
}

// Experiment extracts the typed Experiment from the Evidence envelope.
func (ev *Evidence) Experiment() (*Experiment, error) {
	if ev == nil {
		return nil, errors.New("evidence cannot be nil")
	}
	if ev.Payload != nil {
		return ev.Payload, nil
	}
	if len(ev.PayloadBytes) == 0 {
		return nil, errors.New("empty payload bytes in evidence")
	}
	var exp Experiment
	if err := json.Unmarshal(ev.PayloadBytes, &exp); err != nil {
		return nil, fmt.Errorf("unmarshaling experiment from payload: %w", err)
	}
	return &exp, nil
}

// SerializeEvidence formats the full Evidence envelope into indented JSON bytes.
func SerializeEvidence(ev *Evidence) ([]byte, error) {
	if ev == nil {
		return nil, errors.New("evidence cannot be nil")
	}
	data, err := json.Marshal(ev, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return nil, fmt.Errorf("serializing evidence: %w", err)
	}
	return data, nil
}

// DeserializeEvidence decodes raw JSON bytes into an Evidence envelope.
func DeserializeEvidence(data []byte) (*Evidence, error) {
	var ev Evidence
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("deserializing evidence: %w", err)
	}
	return &ev, nil
}
