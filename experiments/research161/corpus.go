package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// writeCorpusManifest records the exact bytes produced by the standalone Task recipe.
func writeCorpusManifest(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("research161 manifest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("directory", "", "existing directory with demo/microfat v1-v3 files")
	revision := flags.String("source-revision", "", "exact source commit used for the builds")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *directory == "" || *revision == "" {
		return errors.New("manifest requires --directory and --source-revision, without positional arguments")
	}
	root, err := filepath.Abs(*directory)
	if err != nil {
		return err
	}
	corpus := manifest{SchemaVersion: 1, SourceRevision: *revision, GoVersion: runtime.Version()}
	for _, family := range []string{"microfat", "demo"} {
		for _, level := range []string{"v1", "v2", "v3"} {
			path := filepath.Join(root, family+"-"+level)
			data, err := readBounded(path)
			if err != nil {
				return err
			}
			if err := validateELF(data, "amd64"); err != nil {
				return err
			}
			corpus.Variants = append(corpus.Variants, input{
				Family: family, Level: level, Path: path, Bytes: len(data), SHA256: digest(data),
			})
		}
	}
	data, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return err
	}
	// #nosec G304 -- Explicit output directory; exclusive creation refuses an existing manifest or symlink.
	f, err := os.OpenFile(filepath.Join(root, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFile)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	return errors.Join(writeErr, f.Close())
}
