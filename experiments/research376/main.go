package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	json "encoding/json/v2"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
)

const maxInputBytes = 16 << 20

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("research376", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("input", "", "Bounded explicit observation/scenario JSON (no workloads are executed)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("--input is required; positional arguments are not accepted")
	}
	data, err := readInput(*path)
	if err != nil {
		return err
	}
	var in input
	if err := json.Unmarshal(data, &in, json.RejectUnknownMembers(true)); err != nil {
		return fmt.Errorf("reading research input: %w", err)
	}
	result, err := analyze(in)
	if err != nil {
		return err
	}
	return json.MarshalWrite(stdout, result)
}

func readInput(path string) ([]byte, error) {
	file, err := inputfile.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxInputBytes {
		return nil, errors.New("research input must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputBytes {
		return nil, errors.New("research input grew beyond limit")
	}
	return data, nil
}
