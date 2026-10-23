package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
)

func prepareStudyCampaign(opts campaignOptions, m corpusManifest, families []string) (
	campaignOptions, corpusManifest, []string, error,
) {
	if opts.Study != "" {
		if err := validateStudyCorpus(opts, m, families); err != nil {
			return opts, m, families, err
		}
		m = corpusProfile(m, opts.Profile)
	} else if m.SchemaVersion == portableCorpusSchema {
		if opts.Profile == "" {
			opts.Profile = profileStripped
		}
		m = corpusProfile(m, opts.Profile)
		if opts.Matrix {
			opts.MatrixMemoryMiB = resourceMaximumMemory / mib
		}
	}
	if opts.Family != "" {
		if opts.Study != "" || !slices.Contains(families, opts.Family) {
			return opts, m, families, errors.New("family restriction is only for known correctness cohorts")
		}
		families = []string{opts.Family}
		var selected []corpusVariant
		for _, v := range m.Variants {
			if v.Family == opts.Family {
				selected = append(selected, v)
			}
		}
		m.Variants = selected
	}
	return opts, m, families, nil
}

type nativeStudyCheck func(context.Context, string, corpusManifest, string, string) ([]launchObservation, error)

func checkStudyNative(ctx context.Context, r *campaignReport, m corpusManifest, check nativeStudyCheck) error {
	for i := range r.Observations {
		o := &r.Observations[i]
		if !o.Spec.Primary || o.Spec.Block != 0 || !summarySucceeded(*o) {
			continue
		}
		launches, err := check(ctx, o.Request.Output, m, o.Spec.Family, filepath.Dir(o.Request.Output))
		o.Launches = launches
		if err != nil {
			o.Error = err.Error()
			return err
		}
	}
	return nil
}
