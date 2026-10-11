//go:build linux

package cigate

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const benchmarkControlsFailure = 73

// The fake privilege boundary maps only the script's root and manager to a
// private directory. Kernel control files are virtual, so they do not count as
// regular directory entries when the fake rmdir checks whether a cgroup is empty.
const benchmarkControlsSudo = `#!/bin/bash
set -euo pipefail
IFS=$'\n\t'
operation="${1}"
shift
if [[ "${operation}" == mkdir && ! -f "${TEST_STATE}/root" ]]; then
    case "${1}" in
        /sys/fs/cgroup/microfat-ci-controls-test-1-[0-9]*) ;;
        *) printf 'unexpected root\n' > "${TEST_STATE}/violation"; exit 91;;
    esac
    printf '%s\n' "${1}" > "${TEST_STATE}/root"
fi
root="$(cat "${TEST_STATE}/root")"
target="${1}"
if [[ "${operation}" == chown ]]; then
    shift
    target="${1}"
fi
for path in "$@"; do
    case "${path}" in
        "${root}"|"${root}/manager"|"${root}/cgroup.procs"|"${root}/cgroup.subtree_control") ;;
        *) printf '%s\n' "${path}" > "${TEST_STATE}/violation"; exit 91;;
    esac
done
relative="${target#"${root}"}"
printf '%s %s\n' "${operation}" "${relative:-root}" >> "${TEST_STATE}/operations"
case "${operation}" in
    mkdir)
        if [[ "${relative}" == '' && "${TEST_FAILURE}" == preexisting ]]; then
            mkdir -p "${TEST_STATE}/delegated/manager"
            printf 'unowned\n' > "${TEST_STATE}/delegated/sentinel"
            exit "${TEST_EXIT}"
        fi
        if [[ "${relative}" == '' && "${TEST_FAILURE}" == root-mkdir ]]; then exit "${TEST_EXIT}"; fi
        if [[ "${relative}" == /manager && "${TEST_FAILURE}" == manager-mkdir ]]; then exit "${TEST_EXIT}"; fi
        mkdir "${TEST_STATE}/delegated${relative}";;
    tee)
        if [[ "${TEST_FAILURE}" == enable-controllers ]]; then exit "${TEST_EXIT}"; fi
        cat > "${TEST_STATE}/subtree-control";;
    chown)
        if [[ "${TEST_FAILURE}" == ownership ]]; then exit "${TEST_EXIT}"; fi;;
    rmdir)
        if [[ "${relative}" == /manager && "${TEST_FAILURE}" == cleanup ]]; then exit 79; fi
        rmdir "${TEST_STATE}/delegated${relative}";;
    *) printf '%s\n' "${operation}" > "${TEST_STATE}/violation"; exit 91;;
esac
`

const benchmarkControlsGo = `#!/bin/bash
set -euo pipefail
IFS=$'\n\t'
test "${#}" = 4
test "${1}" = run
test "${2}" = ./internal/cmd/benchmark-tools
test "${3}" = controls
test "${4}" = "$(cat "${TEST_STATE}/root")"
test -d "${TEST_STATE}/delegated/manager"
printf 'called\n' > "${TEST_STATE}/tool"
if [[ "${TEST_FAILURE}" == tool || "${TEST_FAILURE}" == cleanup ]]; then exit "${TEST_EXIT}"; fi
printf '{"synthetic_controls":true}\n'
`

type benchmarkControlsFixture struct {
	state, tools, output, environment string
}

func newBenchmarkControlsFixture(t *testing.T) benchmarkControlsFixture {
	t.Helper()
	state := t.TempDir()
	tools := filepath.Join(state, "tools")
	require.NoError(t, os.Mkdir(tools, 0o755))
	for name, script := range map[string]string{
		"sudo": benchmarkControlsSudo,
		"stat": "#!/bin/bash\nprintf 'cgroup2fs\\n'\n",
		"go":   benchmarkControlsGo,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(tools, name), []byte(script), 0o755))
	}
	unrelated := filepath.Join(state, "unrelated")
	require.NoError(t, os.Mkdir(unrelated, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(unrelated, "sentinel"), []byte("retain\n"), 0o644))
	return benchmarkControlsFixture{
		state: state, tools: tools,
		output: filepath.Join(state, "evidence", "controls.json"), environment: filepath.Join(state, "github-env"),
	}
}

func (f benchmarkControlsFixture) run(t *testing.T, failure string, failStdout bool) (int, string, string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "bash", "../../scripts/benchmark-controls.sh", f.output)
	cmd.Env = []string{
		"PATH=" + f.tools + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GO=" + filepath.Join(f.tools, "go"), "GITHUB_ENV=" + f.environment,
		"GITHUB_RUN_ID=controls-test", "GITHUB_RUN_ATTEMPT=1", "TEST_STATE=" + f.state,
		"TEST_FAILURE=" + failure, fmt.Sprintf("TEST_EXIT=%d", benchmarkControlsFailure),
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if failStdout {
		// Linux's failing device makes the script's own printf fail without races
		// between a pipe reader and the child process.
		full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, full.Close()) })
		cmd.Stdout = full
	}
	err := cmd.Run()
	if err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, "script must exit normally: %s", stderr.String())
	}
	return cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

func (f benchmarkControlsFixture) operations(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.state, "operations"))
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (f benchmarkControlsFixture) assertConfined(t *testing.T) {
	t.Helper()
	assert.NoFileExists(t, filepath.Join(f.state, "violation"), "privileged operations must stay in the owned subtree")
	data, err := os.ReadFile(filepath.Join(f.state, "unrelated", "sentinel"))
	require.NoError(t, err)
	assert.Equal(t, "retain\n", string(data))
}

func TestBenchmarkControlsRollsBackEverySetupFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, failure string
		status        int
		configure     func(*testing.T, *benchmarkControlsFixture)
		failStdout    bool
	}{
		{name: "enable controllers", failure: "enable-controllers", status: benchmarkControlsFailure},
		{name: "assign ownership", failure: "ownership", status: benchmarkControlsFailure},
		{name: "create manager", failure: "manager-mkdir", status: benchmarkControlsFailure},
		{name: "execute controls tool", failure: "tool", status: benchmarkControlsFailure},
		{name: "create output directory", status: 1, configure: func(t *testing.T, f *benchmarkControlsFixture) {
			require.NoError(t, os.WriteFile(filepath.Join(f.state, "evidence"), []byte("blocked"), 0o644))
		}},
		{name: "open controls output", status: 1, configure: func(t *testing.T, f *benchmarkControlsFixture) {
			require.NoError(t, os.MkdirAll(f.output, 0o755))
		}},
		{name: "write controls output", status: 1, configure: func(_ *testing.T, f *benchmarkControlsFixture) {
			f.output = "/dev/full"
		}},
		{name: "open environment export", status: 1, configure: func(t *testing.T, f *benchmarkControlsFixture) {
			require.NoError(t, os.Mkdir(f.environment, 0o755))
		}},
		{name: "write environment export", status: 1, configure: func(_ *testing.T, f *benchmarkControlsFixture) {
			f.environment = "/dev/full"
		}},
		{name: "write root output", status: 1, failStdout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newBenchmarkControlsFixture(t)
			if tc.configure != nil {
				tc.configure(t, &fixture)
			}
			status, _, stderr := fixture.run(t, tc.failure, tc.failStdout)
			assert.Equal(t, tc.status, status, stderr)
			operations := fixture.operations(t)
			require.GreaterOrEqual(t, len(operations), 2)
			assert.Equal(t, []string{"rmdir /manager", "rmdir root"}, operations[len(operations)-2:])
			assert.NoDirExists(t, filepath.Join(fixture.state, "delegated"), "failed setup must release its subtree")
			fixture.assertConfined(t)
			if tc.failStdout {
				assert.NoFileExists(t, fixture.environment, "failed root output must not export a removed cgroup")
			}
		})
	}
}

func TestBenchmarkControlsCleanupPreservesFailureAndAttemptsParent(t *testing.T) {
	t.Parallel()
	fixture := newBenchmarkControlsFixture(t)
	status, _, stderr := fixture.run(t, "cleanup", false)
	assert.Equal(t, benchmarkControlsFailure, status, stderr)
	operations := fixture.operations(t)
	require.GreaterOrEqual(t, len(operations), 2)
	assert.Equal(t, []string{"rmdir /manager", "rmdir root"}, operations[len(operations)-2:])
	assert.DirExists(t, filepath.Join(fixture.state, "delegated", "manager"))
	fixture.assertConfined(t)
}

func TestBenchmarkControlsLeavesUnownedRootsUntouched(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"root-mkdir", "preexisting"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			fixture := newBenchmarkControlsFixture(t)
			status, _, stderr := fixture.run(t, failure, false)
			assert.Equal(t, benchmarkControlsFailure, status, stderr)
			assert.Equal(t, []string{"mkdir root"}, fixture.operations(t), "failed root creation never transfers ownership")
			fixture.assertConfined(t)
			if failure == "preexisting" {
				data, err := os.ReadFile(filepath.Join(fixture.state, "delegated", "sentinel"))
				require.NoError(t, err)
				assert.Equal(t, "unowned\n", string(data))
				assert.DirExists(t, filepath.Join(fixture.state, "delegated", "manager"))
			}
		})
	}
}

func TestBenchmarkControlsSuccessfulSetupHandsOffUsableEnvironment(t *testing.T) {
	t.Parallel()
	fixture := newBenchmarkControlsFixture(t)
	status, stdout, stderr := fixture.run(t, "", false)
	require.Zero(t, status, stderr)
	root, err := os.ReadFile(filepath.Join(fixture.state, "root"))
	require.NoError(t, err)
	assert.Equal(t, string(root), stdout)
	assert.DirExists(t, filepath.Join(fixture.state, "delegated", "manager"))
	for _, operation := range fixture.operations(t) {
		assert.NotContains(t, operation, "rmdir")
	}
	controls, err := os.ReadFile(fixture.output)
	require.NoError(t, err)
	assert.JSONEq(t, `{"synthetic_controls":true}`, string(controls))
	cmd := exec.CommandContext(t.Context(), "bash", "-c",
		`source "$1"; printf '%s\n' "$MICROFAT_BENCH_CGROUP_ROOT" "$MICROFAT_BENCH_CGROUP_VERSION" "$MICROFAT_BENCH_REQUIRE_CONTROLS"`,
		"test-export", fixture.environment)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	exported, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", exported)
	assert.Equal(t, string(root)+"v2\n1\n", string(exported))
	fixture.assertConfined(t)
}
