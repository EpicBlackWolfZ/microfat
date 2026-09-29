package runtimequalify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
)

type controller struct {
	options  Options
	runner   releaseaudit.Runner
	summary  Summary
	products string
	reporter string
	helper   string
	bundles  map[string]Lineage
}

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), dataMode)
}

func Run(ctx context.Context, options Options, out io.Writer, environment []string, execute releaseaudit.Execute) error {
	if err := options.Validate(runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}
	root, err := filepath.Abs(options.Output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, privateMode); err != nil {
		return err
	}
	options.Output, err = os.MkdirTemp(root, "run-")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Runtime qualification evidence:", options.Output); err != nil {
		return err
	}
	c := &controller{options: options, bundles: map[string]Lineage{},
		runner: releaseaudit.Runner{Context: ctx, Output: options.Output,
			Environment: releaseaudit.CleanEnvironment(environment), Execute: execute},
		summary: Summary{Schema: Schema, Status: Incomplete, Input: options.Input, Architecture: runtime.GOARCH, Backend: options.Backend,
			Tag: options.Tag, PageSize: os.Getpagesize(), Expected: Cases(), Products: map[string]string{},
			RunID: envValue(environment, "GITHUB_RUN_ID"), Attempt: envValue(environment, "GITHUB_RUN_ATTEMPT")}}
	if err := c.save(); err != nil {
		return err
	}
	err = c.qualify(out)
	if err != nil {
		c.summary.Error = err.Error()
		c.summary.Status = Fail
	}
	if err == nil && c.summary.Status != Incomplete {
		c.summary.Status = Pass
		if validationErr := ValidateSummary(c.summary); validationErr != nil {
			err = validationErr
			c.summary.Status = Fail
		}
	}
	return errors.Join(err, c.save())
}

func (c *controller) save() error {
	return WriteJSON(filepath.Join(c.options.Output, "summary.json"), c.summary)
}

func envValue(environment []string, name string) string {
	for _, value := range environment {
		if v, ok := strings.CutPrefix(value, name+"="); ok {
			return v
		}
	}
	return ""
}

func (c *controller) qualify(out io.Writer) error {
	var err error
	c.summary.Toolchain, err = c.runner.Run([]string{c.options.Go, "version"}, nil, true)
	if err != nil {
		return err
	}
	c.summary.Toolchain = strings.TrimSpace(c.summary.Toolchain)
	if !strings.HasPrefix(c.summary.Toolchain, "go version go1.27.1 ") {
		return errors.New("Go 1.27.1 required")
	}
	c.summary.Source, err = c.runner.Run([]string{"git", "rev-parse", "HEAD"}, nil, true)
	if err != nil {
		return err
	}
	c.summary.Source = strings.TrimSpace(c.summary.Source)
	status, err := c.runner.Run([]string{"git", "status", "--porcelain"}, nil, true)
	if err != nil {
		return err
	}
	c.summary.Dirty = status != ""
	c.summary.Kernel, err = c.runner.Run([]string{"uname", "-srmo"}, nil, true)
	if err != nil {
		return err
	}
	if !nativeKernel(c.summary.Kernel, c.summary.Architecture) {
		return errors.New("native kernel architecture does not match controller")
	}
	policy, policyErr := os.ReadFile("/proc/sys/vm/memfd_noexec")
	c.summary.Policy = strings.TrimSpace(string(policy))
	if policyErr != nil {
		c.summary.Policy = "unavailable: " + policyErr.Error()
	}
	if err := c.save(); err != nil {
		return err
	}
	if os.Getuid() == 0 || os.Getgid() == 0 {
		return errors.New("invoke runtime qualification as an ordinary user")
	}
	missing, err := c.preflight()
	if err != nil {
		c.summary.Error = "namespace prerequisite unavailable: " + err.Error()
		if c.options.Tests == Required || !missing {
			return errors.New(c.summary.Error)
		}
		_, writeErr := fmt.Fprintln(out, "INCOMPLETE:", c.summary.Error)
		return writeErr
	}
	if c.summary.Dirty {
		if err := c.retainSource(); err != nil {
			return err
		}
	}
	if err := c.acquire(); err != nil {
		return err
	}
	if err := c.buildHelpers(); err != nil {
		return err
	}
	for _, configuration := range Configurations() {
		lineage, err := c.pack(configuration)
		if err != nil {
			return err
		}
		c.bundles[configuration.Name()] = lineage
	}
	return c.runCases(out)
}

func (c *controller) runCases(out io.Writer) error {
	for _, item := range c.summary.Expected {
		entry, err := c.exercise(item)
		if err != nil {
			entry.Reason, entry.Status = err.Error(), Fail
		} else {
			entry.Status = Pass
		}
		c.summary.Results = append(c.summary.Results, entry)
		name := strings.ReplaceAll(item.ID, "/", "__") + ".json"
		if writeErr := WriteJSON(filepath.Join(c.options.Output, name), entry); writeErr != nil {
			return writeErr
		}
		if writeErr := c.save(); writeErr != nil {
			return writeErr
		}
		if _, writeErr := fmt.Fprintf(out, "%s: %s\n", item.ID, entry.Status); writeErr != nil {
			return writeErr
		}
		if err != nil {
			return fmt.Errorf("%s: %w", item.ID, err)
		}
		// Successful requests are reconstructible from retained products and JSON.
		// Failed fixtures are retained for a minimized reproduction.
		if err := os.RemoveAll(entry.Request.Root); err != nil {
			return err
		}
	}
	c.summary.Status = Pass
	return nil
}

func (c *controller) preflight() (bool, error) {
	runner := c.runner
	var result releaseaudit.Result
	runner.Execute = func(ctx context.Context, spec process.Spec) (releaseaudit.Result, error) {
		var err error
		result, err = c.runner.Execute(ctx, spec)
		return result, err
	}
	_, err := runner.Run(namespaceCommand(c.options.Backend, "true"), nil, true)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true, err
	}
	if result.ExitCode == 1 && result.Stdout == "" && !errors.Is(err, context.DeadlineExceeded) {
		for _, known := range []string{"unshare: unshare failed: Operation not permitted", "unshare: unshare failed: Permission denied",
			"sudo: a password is required", "sudo: a terminal is required"} {
			if strings.Contains(result.Stderr, known) {
				return true, err
			}
		}
	}
	return false, err
}

func namespaceCommand(backend string, command ...string) []string {
	args := []string{"unshare", "--mount", "--pid", "--fork", "--kill-child=SIGKILL", "--mount-proc", "--propagation", "private"}
	if backend == Sudo {
		args = append([]string{Sudo, "-n", "--"}, args...)
	} else {
		args = append(args, "--user", "--map-current-user", "--keep-caps")
	}
	return append(append(args, "--"), command...)
}

func (c *controller) retainSource() error {
	patch, err := c.runner.Run([]string{"git", "diff", "--binary", "HEAD"}, nil, true)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(c.options.Output, "source.patch"), []byte(patch), dataMode); err != nil {
		return err
	}
	list, err := c.runner.Run([]string{"git", "ls-files", "--others", "--exclude-standard", "-z"}, nil, true)
	if err != nil {
		return err
	}
	for _, name := range strings.Split(list, "\x00") {
		if name == "" {
			continue
		}
		if !filepath.IsLocal(name) {
			return errors.New("unsafe source archive path")
		}
		target := filepath.Join(c.options.Output, "untracked-source", name)
		if err := os.MkdirAll(filepath.Dir(target), privateMode); err != nil {
			return err
		}
		if err := copyFile(name, target, dataMode, false); err != nil {
			return err
		}
	}
	return nil
}

func (c *controller) exercise(item Case) (Evidence, error) {
	entry := Evidence{Case: item, Status: Fail, Lineage: c.bundles[item.Configuration.Name()]}
	var err error
	if item.Scenario == distributedCLI {
		entry.Lineage, err = c.cliLineage()
		if err != nil {
			return entry, err
		}
	}
	if item.Scenario == policyControl {
		entry.Lineage = Lineage{Bundle: c.reporter, BundleSHA256: c.summary.Products["reporter"]}
	}
	entry.Request, err = c.request(item, entry.Lineage)
	if err != nil {
		return entry, err
	}
	entry.ExecutedSHA256, err = releaseaudit.Digest(filepath.Join(entry.Request.Root, "source/app"))
	if err != nil {
		return entry, err
	}
	requestFile := filepath.Join(entry.Request.Root, "request.json")
	if err := WriteJSON(requestFile, entry.Request); err != nil {
		return entry, err
	}
	entry.Command = namespaceCommand(c.options.Backend, c.helper, requestFile)
	start := time.Now()
	ctx, cancel := context.WithTimeout(c.runner.Context, commandTimeout)
	defer cancel()
	result, err := c.runner.Execute(ctx, process.Spec{Path: entry.Command[0], Args: entry.Command[1:], Env: c.runner.Environment})
	entry.Duration, entry.Stderr = time.Since(start), result.Stderr
	decodeErr := json.Unmarshal([]byte(result.Stdout), &entry.Result)
	if err != nil || decodeErr != nil || result.ExitCode != 0 || ctx.Err() != nil {
		return entry, errors.Join(err, decodeErr, ctx.Err(), fmt.Errorf("fixture exit %d, stage %s: %s",
			result.ExitCode, entry.Result.Stage, entry.Result.Error))
	}
	return entry, ValidateEvidence(entry)
}

func copyFile(source, target string, mode os.FileMode, link bool) error {
	if link {
		if err := os.Link(source, target); err == nil {
			return nil
		}
	}
	// #nosec G304 -- explicit source artifact or retained source file selected by this controller.
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	// #nosec G304 -- exclusive creation inside the controller's private evidence or fixture directory.
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Close())
}
