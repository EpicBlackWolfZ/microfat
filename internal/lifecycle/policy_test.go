package lifecycle_test

import (
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input       string
		expected    lifecycle.MetadataPolicy
		expectError bool
	}{
		{"strict", lifecycle.PolicyStrict, false},
		{"STRICT", lifecycle.PolicyStrict, false},
		{"  strict  ", lifecycle.PolicyStrict, false},
		{"", lifecycle.PolicyStrict, false},
		{"strip", lifecycle.PolicyStrip, false},
		{"STRIP", lifecycle.PolicyStrip, false},
		{"  strip  ", lifecycle.PolicyStrip, false},
		{"invalid", "", true},
		{"none", "", true},
		{"auto", "", true},
	}

	for _, tt := range tests {
		t.Run("parse_"+tt.input, func(t *testing.T) {
			t.Parallel()
			got, err := lifecycle.ParsePolicy(tt.input)
			if tt.expectError {
				require.Error(t, err)
				require.ErrorIs(t, err, lifecycle.ErrInvalidMetadataPolicy)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()

	opts := lifecycle.DefaultOptions()
	assert.Equal(t, lifecycle.PolicyStrict, opts.Policy)
	assert.False(t, opts.BreakHardlinks)
}
