package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	json "encoding/json/v2"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("research380", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "", "prepare, run, analyze or iid-sensitivity")
	input := flags.String("request", "", "bounded request or report JSON")
	output := flags.String("output", "", "new private campaign directory (run mode)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *input == "" || flags.NArg() != 0 {
		return errors.New("--request is required; no positional arguments")
	}
	data, err := readJSON(*input)
	if err != nil {
		return err
	}
	switch *mode {
	case "prepare":
		if !filepath.IsAbs(*output) {
			return errors.New("--output must be an absolute new directory")
		}
		var in prepareRequest
		if err := json.Unmarshal(data, &in, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		result, err := prepareArtifacts(context.Background(), in, *output, execute)
		if err != nil {
			return err
		}
		return json.MarshalWrite(stdout, result)
	case "run":
		if !filepath.IsAbs(*output) {
			return errors.New("--output must be an absolute new directory")
		}
		var in request
		if err := json.Unmarshal(data, &in, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		result, err := runCampaign(context.Background(), in, hex.EncodeToString(digest[:]), *output, execute)
		if err != nil {
			return err
		}
		return json.MarshalWrite(stdout, result)
	case "analyze":
		var in report
		if err := json.Unmarshal(data, &in, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		result, err := analyzeReport(in)
		if err != nil {
			return err
		}
		return json.MarshalWrite(stdout, result)
	case "iid-sensitivity":
		var in report
		if err := json.Unmarshal(data, &in, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		result, err := analyzeSensitivity(in, textSHA(string(data)))
		if err != nil {
			return err
		}
		return json.MarshalWrite(stdout, result)
	default:
		return errors.New("--mode must be prepare, run, analyze or iid-sensitivity")
	}
}

func textSHA(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func openBounded(path string, limit int64) (*os.File, error) {
	file, err := inputfile.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		_ = file.Close()
		return nil, errors.New("input must be a bounded regular file")
	}
	return file, nil
}

func readJSON(path string) ([]byte, error) {
	file, err := openBounded(path, maximumInput)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maximumInput+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximumInput {
		return nil, errors.New("JSON grew beyond bound")
	}
	return data, nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), privateFileMode)
}
