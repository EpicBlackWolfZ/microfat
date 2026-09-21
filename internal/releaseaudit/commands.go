// Package releaseaudit authenticates published artifacts before native functional testing.
package releaseaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
)

const commandTimeout = 2 * time.Minute
const fileMode = 0o600
const directoryMode = 0o700

type Result struct {
	ExitCode       int
	Stdout, Stderr string
}
type Execute func(context.Context, process.Spec) (Result, error)
type Runner struct {
	Context     context.Context
	Output      string
	Environment []string
	Execute     Execute
}

type CommandRecord struct {
	Arguments   []string          `json:"arguments"`
	Environment map[string]string `json:"environment"`
	Exit        int               `json:"exit"`
	Seconds     float64           `json:"seconds"`
	Stdout      string            `json:"stdout"`
	Stderr      string            `json:"stderr"`
	Timeout     bool              `json:"timeout,omitempty"`
	Error       string            `json:"error,omitempty"`
}

func CleanEnvironment(environment []string) []string {
	var result []string
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "MICROFAT_") || key == "GOGC" || key == "GOMEMLIMIT" || key == "GOMAXPROCS" {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (r Runner) Run(arguments []string, overrides map[string]string, success bool) (string, error) {
	if len(arguments) == 0 {
		return "", errors.New("empty audit command")
	}
	started := time.Now()
	parent := r.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, commandTimeout)
	defer cancel()
	environment := make([]string, 0, len(r.Environment)+len(overrides))
	for _, entry := range r.Environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	result, err := r.Execute(ctx, process.Spec{Path: arguments[0], Args: arguments[1:], Env: environment})
	record := CommandRecord{Arguments: arguments, Environment: overrides, Exit: result.ExitCode, Seconds: time.Since(started).Seconds(),
		Stdout: result.Stdout, Stderr: result.Stderr, Timeout: errors.Is(ctx.Err(), context.DeadlineExceeded)}
	if err != nil {
		record.Error = err.Error()
	}
	if recordErr := r.record(record); recordErr != nil {
		return "", recordErr
	}
	if err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if (success && result.ExitCode != 0) || (!success && result.ExitCode <= 0) {
		return "", fmt.Errorf("unexpected exit %d: %v", result.ExitCode, arguments)
	}
	if strings.Contains(result.Stderr, "panic:") || strings.Contains(result.Stderr, "fatal error:") {
		return "", errors.New("downloaded command crashed")
	}
	return result.Stdout, nil
}

func (r Runner) record(record CommandRecord) error {
	file, err := os.OpenFile(filepath.Join(r.Output, "commands.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(file).Encode(record), file.Close())
}

func ExecuteProcess(ctx context.Context, spec process.Spec) (Result, error) {
	child, err := process.Start(ctx, spec)
	if err != nil {
		return Result{}, err
	}
	err = child.Wait()
	result := Result{ExitCode: child.State().ExitCode(), Stdout: string(child.Stdout()), Stderr: string(child.Stderr())}
	if onlyExitStatus(err) {
		err = nil
	}
	return result, errors.Join(err, ctx.Err())
}

// An expected nonzero exit is distinct from drain, cleanup, output or transport failure.
func onlyExitStatus(err error) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, part := range joined.Unwrap() {
			if !onlyExitStatus(part) {
				return false
			}
		}
		return true
	}
	_, ok := err.(*exec.ExitError)
	return ok
}
