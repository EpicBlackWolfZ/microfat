//go:build linux

package lifecycle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecute_HardLinkCreatedDuringTransform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		intent         lifecycle.PublicationIntent
		breakHardlinks bool
		wantRefusal    bool
	}{
		{
			name:        "replace_default_refuses",
			intent:      lifecycle.IntentReplaceSource,
			wantRefusal: true,
		},
		{
			name:           "replace_explicit_break_succeeds",
			intent:         lifecycle.IntentReplaceSource,
			breakHardlinks: true,
		},
		{
			name:   "create_only_preserves_links",
			intent: lifecycle.IntentCreateOnly,
		},
	}
	inputs := []struct {
		name       string
		descriptor bool
	}{
		{name: "path"},
		{name: "descriptor", descriptor: true},
	}

	for _, tt := range tests {
		for _, policy := range []lifecycle.MetadataPolicy{lifecycle.PolicyStrict, lifecycle.PolicyStrip} {
			for _, input := range inputs {
				t.Run(tt.name+"/"+string(policy)+"/"+input.name, func(t *testing.T) {
					t.Parallel()

					dir := t.TempDir()
					original := []byte("original executable payload")
					transformed := []byte("transformed executable payload")
					src := createTestBinary(t, dir, "source.bin", original)
					alias := filepath.Join(dir, "alias.bin")
					before, err := os.Stat(src)
					require.NoError(t, err)

					tx := lifecycle.Transaction{
						SrcPath: src,
						Intent:  tt.intent,
						Opts: lifecycle.Options{
							Policy:         policy,
							BreakHardlinks: tt.breakHardlinks,
						},
						Transform: func(staged *os.File) error {
							// Execute has already captured its source snapshot when this runs.
							if linkErr := os.Link(src, alias); linkErr != nil {
								return linkErr
							}
							_, writeErr := staged.Write(transformed)
							return writeErr
						},
					}
					if tt.intent == lifecycle.IntentCreateOnly {
						tx.DestPath = filepath.Join(dir, "destination.bin")
					}
					if input.descriptor {
						sourceFile, openErr := os.Open(src)
						require.NoError(t, openErr)
						t.Cleanup(func() { require.NoError(t, sourceFile.Close()) })
						tx.SrcFile = sourceFile
					}

					err = lifecycle.Execute(tx)
					if tt.wantRefusal {
						require.ErrorIs(t, err, lifecycle.ErrHardLinkDetected)
					} else {
						require.NoError(t, err)
					}

					aliasData, err := os.ReadFile(alias)
					require.NoError(t, err)
					assert.Equal(t, original, aliasData)
					aliasInfo, err := os.Stat(alias)
					require.NoError(t, err)
					assert.True(t, os.SameFile(before, aliasInfo), "alias must retain the original inode")
					sourceData, err := os.ReadFile(src)
					require.NoError(t, err)
					sourceInfo, err := os.Stat(src)
					require.NoError(t, err)

					if tt.intent == lifecycle.IntentReplaceSource && !tt.wantRefusal {
						assert.Equal(t, transformed, sourceData)
						assert.False(t, os.SameFile(sourceInfo, aliasInfo), "explicit breaking must replace the source inode")
					} else {
						assert.Equal(t, original, sourceData)
						assert.True(t, os.SameFile(sourceInfo, aliasInfo), "source and alias must remain linked")
					}
					if tt.intent == lifecycle.IntentCreateOnly {
						destinationData, readErr := os.ReadFile(tx.DestPath)
						require.NoError(t, readErr)
						assert.Equal(t, transformed, destinationData)
						destinationInfo, statErr := os.Stat(tx.DestPath)
						require.NoError(t, statErr)
						assert.False(t, os.SameFile(destinationInfo, sourceInfo), "derivative must have its own inode")
					}
					stagingFiles, err := filepath.Glob(filepath.Join(dir, ".microfat-tx-*.tmp"))
					require.NoError(t, err)
					assert.Empty(t, stagingFiles, "transaction must remove staging files")
				})
			}
		}
	}
}
