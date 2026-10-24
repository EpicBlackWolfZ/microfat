package runtimeinfo

import (
	"encoding/hex"
	"math"
	"strconv"
	"strings"
)

const (
	decimalBase = 10
	integerBits = 64
	digestBytes = 32
)

func textField(lookup lookupEnv, key string) Field[string] {
	raw, ok := lookup(key)
	status := StatusMissing
	if ok {
		status = StatusPresent
	}
	return Field[string]{Value: raw, Raw: raw, Status: status, Source: key}
}

func enumField(lookup lookupEnv, key string, supported ...string) Field[string] {
	f := textField(lookup, key)
	if f.Status != StatusPresent {
		return f
	}
	for _, value := range supported {
		if f.Raw == value {
			return f
		}
	}
	f.Value, f.Status = "", StatusUnsupported
	return f
}

func digestField(lookup lookupEnv, key string) Field[string] {
	f := textField(lookup, key)
	if f.Status != StatusPresent {
		return f
	}
	decoded, err := hex.DecodeString(f.Raw)
	if err != nil || len(decoded) != digestBytes {
		f.Value, f.Status = "", StatusInvalid
	} else {
		f.Value = strings.ToLower(f.Raw)
	}
	return f
}

func integerField(lookup lookupEnv, key string, minimum int64) Field[int64] {
	raw, exists := lookup(key)
	f := Field[int64]{Raw: raw, Source: key, Status: StatusMissing}
	if !exists {
		return f
	}
	f.Status = StatusInvalid
	if !decimalDigits(raw) {
		return f
	}
	value, err := strconv.ParseInt(raw, decimalBase, integerBits)
	if err == nil && value >= minimum {
		f.Value, f.Status = value, StatusPresent
	}
	return f
}

func decimalDigits(raw string) bool {
	if raw == "" {
		return false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func gcField(lookup lookupEnv, key string) Field[int64] {
	f := integerField(lookup, key, 0)
	if f.Raw == "off" {
		f.Value, f.Status = -1, StatusPresent
	}
	return f
}

func quotaField(lookup lookupEnv, key string) Field[float64] {
	raw, exists := lookup(key)
	f := Field[float64]{Raw: raw, Source: key, Status: StatusMissing}
	if !exists {
		return f
	}
	f.Status = StatusInvalid
	whole, fraction, dot := strings.Cut(raw, ".")
	if !decimalDigits(whole) || dot && !decimalDigits(fraction) {
		return f
	}
	value, err := strconv.ParseFloat(raw, integerBits)
	if err == nil && !math.IsInf(value, 0) && !math.IsNaN(value) {
		f.Value, f.Status = value, StatusPresent
	}
	return f
}
