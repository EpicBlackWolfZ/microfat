package main

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const missingPath = "/missing"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "_worker":
			os.Exit(entry())
		case "_ready":
			_, _ = io.WriteString(os.Stdout, "READY\n")
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func writeTest(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "input")
	require.NoError(t, os.WriteFile(p, data, privateMode))
	return p
}

func baseArgs(path string) []string {
	return []string{"--input=" + path, "--family=test", "--source-revision=test-source", "--trials=1",
		"--encoders=1", "--workers=1", "--decoders=1", "--bursts=1"}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	c, err := parse(baseArgs("file"))
	require.NoError(t, err)
	assert.Equal(t, []int{1}, c.Decoders)
	assert.Equal(t, "file", c.Inputs.String())
	for _, arg := range []string{"--unknown", "--cpu-budget=0", "--cpu-budget=5", "--trials=0", "--trials=21",
		"--timeout=0s", "--timeout=31s", "--arrival-gap=-1s", "--arrival-gap=2s", "--execute",
		"--ready-output=" + strings.Repeat("x", maxOutputBytes+1), "--encoders=0", "--decoders=1,1", "--workers=-1",
		"--bursts=word", "positional"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()
			_, err := parse(append(baseArgs("file"), arg))
			require.Error(t, err)
		})
	}
	_, err = parse(nil)
	require.Error(t, err)
	_, err = integers("-1,1,2,4", true)
	require.NoError(t, err)
	assert.Equal(t, runtime.GOMAXPROCS(0), effective(-1, false))
	assert.Equal(t, min(runtime.GOMAXPROCS(0), maxParallel), effective(-1, true))
	assert.Equal(t, 2, effective(2, true))
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("injected output failure") }

func TestCodecIntegrity(t *testing.T) {
	t.Parallel()
	data := bytes.Repeat([]byte("hello shared binary data"), 200)
	dict, err := codec.TrainDictionary([][]byte{data, append([]byte("prefix"), data...)}, 1024, "default")
	require.NoError(t, err)
	for _, dictionary := range [][]byte{nil, dict} {
		for _, threads := range []int{-1, 1, 2, 4} {
			encoded, err := encode(data, dictionary, "default", threads)
			require.NoError(t, err)
			decoded, duration, err := decode(encoded, dictionary, len(data), digest(data), threads)
			require.NoError(t, err)
			assert.Equal(t, data, decoded)
			assert.Positive(t, duration)
			_, _, err = decode(encoded, dictionary, len(data), "wrong hash", threads)
			require.Error(t, err)
			_, _, err = decode(encoded, dictionary, len(data)-1, digest(data), threads)
			require.Error(t, err)
		}
	}
	_, err = encode(data, nil, "invalid", 1)
	require.Error(t, err)
	_, err = encode(data, []byte("bad dictionary"), "default", 1)
	require.Error(t, err)
	_, err = encode(data, nil, "default", -2)
	require.Error(t, err)
	_, _, err = decode(data, nil, len(data), digest(data), 1)
	require.Error(t, err)
	_, _, err = decode(data, []byte("bad dictionary"), len(data), digest(data), 1)
	require.Error(t, err)
	_, _, err = decode(data, nil, len(data), digest(data), -2)
	require.Error(t, err)
	b := boundedBuffer{limit: 1}
	_, err = b.Write([]byte("xx"))
	require.Error(t, err)
}

func TestInputValidation(t *testing.T) {
	t.Parallel()
	_, err := readBounded("/does/not/exist", 1)
	require.Error(t, err)
	_, err = readBounded(t.TempDir(), 1)
	require.Error(t, err)
	_, err = readBounded(writeTest(t, []byte("xx")), 1)
	require.Error(t, err)
	_, err = loadPayload(writeTest(t, []byte("not ELF")))
	require.Error(t, err)
	self, err := os.Executable()
	require.NoError(t, err)
	p, err := loadPayload(self)
	require.NoError(t, err)
	assert.Positive(t, p.Bytes)
	assert.Len(t, p.SHA256, 64)
	data := append([]byte(nil), p.Data...)
	machine := elf.EM_AARCH64
	if runtime.GOARCH == "arm64" {
		machine = elf.EM_X86_64
	}
	binary.LittleEndian.PutUint16(data[18:], uint16(machine))
	_, err = loadPayload(writeTest(t, data))
	require.ErrorContains(t, err, "native")
	// Keep a syntactically valid ELF but remove its Go build-info marker.
	data = bytes.ReplaceAll(p.Data, []byte("\xff Go buildinf:"), []byte(".............."))
	_, err = loadPayload(writeTest(t, data))
	require.ErrorContains(t, err, "real Go ELF")
}

func TestCallerInputsRefuseFIFOWithoutWaitingForWriter(t *testing.T) {
	t.Parallel()
	pipe := filepath.Join(t.TempDir(), "input.fifo")
	require.NoError(t, unix.Mkfifo(pipe, privateMode))
	// If blocking behavior returns, release the reader during cleanup before unlinking.
	t.Cleanup(func() {
		fd, err := unix.Open(pipe, unix.O_WRONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			_ = unix.Close(fd)
		}
	})
	done := make(chan error, 1)
	go func() { _, err := readRegular(pipe, maxInputBytes); done <- err }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, inputfile.ErrNotRegular)
	case <-time.After(time.Second):
		t.Fatal("FIFO input waited for a writer")
	}
	_, err := loadPayload(pipe)
	require.ErrorIs(t, err, inputfile.ErrNotRegular)
	data := []byte("bytes")
	err = operate(context.Background(), request{Mode: modeEncode, Input: writeTest(t, data), Dictionary: pipe}, &observation{})
	require.ErrorIs(t, err, inputfile.ErrNotRegular)
	_, err = readRegular(t.TempDir(), maxInputBytes)
	require.ErrorIs(t, err, inputfile.ErrNotRegular)
	// Procfs reports zero sizes; the telemetry path must remain readable.
	data, err = readBounded("/proc/self/status", workerConfigLimit)
	require.NoError(t, err)
	assert.Contains(t, string(data), "Pid:")
}

func TestWorkerProtocol(t *testing.T) {
	t.Parallel()
	data := []byte("source bytes")
	input := writeTest(t, data)
	frame := filepath.Join(t.TempDir(), "frame")
	valid := request{Mode: modeEncode, Input: input, Output: frame, SHA256: digest(data), Bytes: len(data), Threads: 1}
	marshal := func(r request) string { raw, err := json.Marshal(r); require.NoError(t, err); return writeTest(t, raw) }
	var out bytes.Buffer
	require.NoError(t, worker(context.Background(), marshal(valid), strings.NewReader("x"), &out, io.Discard))
	var o observation
	require.NoError(t, json.Unmarshal(out.Bytes(), &o))
	assert.False(t, o.Failed)
	assert.Positive(t, o.CompressedBytes)
	valid.Mode, valid.Input = modeDecode, frame
	out.Reset()
	require.NoError(t, worker(context.Background(), marshal(valid), strings.NewReader("x"), &out, io.Discard))
	require.NoError(t, json.Unmarshal(out.Bytes(), &o))
	assert.False(t, o.Failed)
	valid.SHA256 = "changed"
	out.Reset()
	require.NoError(t, worker(context.Background(), marshal(valid), strings.NewReader("x"), &out, io.Discard))
	require.NoError(t, json.Unmarshal(out.Bytes(), &o))
	assert.True(t, o.Failed)
	for _, path := range []string{missingPath, writeTest(t, []byte("{")), marshal(request{Bytes: 0}),
		marshal(request{Bytes: 1, Threads: 0})} {
		require.Error(t, worker(context.Background(), path, strings.NewReader("x"), &out, io.Discard))
	}
	require.Error(t, worker(context.Background(), marshal(valid), strings.NewReader(""), &out, io.Discard))
	require.Error(t, worker(context.Background(), marshal(valid), strings.NewReader("x"), &out, brokenWriter{}))
	require.Error(t, worker(context.Background(), marshal(valid), strings.NewReader("x"), brokenWriter{}, io.Discard))
}

func TestOperationErrors(t *testing.T) {
	t.Parallel()
	data := []byte("bytes")
	base := request{Mode: modeEncode, Input: writeTest(t, data), Output: filepath.Join(t.TempDir(), "frame"),
		Bytes: len(data), SHA256: digest(data), Threads: 1}
	for name, mutate := range map[string]func(*request){
		"missing input":      func(r *request) { r.Input = missingPath },
		"missing dictionary": func(r *request) { r.Dictionary = missingPath },
		"input tampering":    func(r *request) { r.SHA256 = "wrong" },
		"bad level":          func(r *request) { r.Level = "nonsense" },
		"bad output":         func(r *request) { r.Output = "/missing/directory/frame" },
		"bad mode":           func(r *request) { r.Mode = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := base
			mutate(&r)
			require.Error(t, operate(context.Background(), r, &observation{}))
		})
	}
	self, err := os.Executable()
	require.NoError(t, err)
	bin, err := os.ReadFile(self)
	require.NoError(t, err)
	require.NoError(t, execute(context.Background(), bin, request{Args: []string{"_ready"}, ReadyOutput: "READY\n"}))
	require.Error(t, execute(context.Background(), bin, request{Args: []string{"_ready"}, ReadyOutput: "WRONG"}))
	require.Error(t, execute(context.Background(), data, request{}))
}

func TestProcessAndBatch(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	data := []byte("real codec input")
	frame, err := encode(data, nil, "default", 1)
	require.NoError(t, err)
	r := request{Mode: modeDecode, Input: writeTest(t, frame), Bytes: len(data), SHA256: digest(data), Threads: 1}
	c := config{Timeout: 5 * time.Second, Gap: time.Millisecond}
	b := runBatch(context.Background(), c, self, t.TempDir(), "block", []request{r, r}, 2)
	require.Len(t, b.Samples, 2)
	for _, o := range b.Samples {
		assert.False(t, o.Failed, o.Error)
		assert.Positive(t, o.DurationNS)
		assert.Positive(t, o.CompletionNS)
		assert.Positive(t, o.MaxRSSKiB)
	}
	assert.Greater(t, b.Samples[1].ReleaseNS, b.Samples[0].ReleaseNS)
	b = runBatch(context.Background(), c, missingPath, t.TempDir(), "failed", []request{r}, 1)
	assert.True(t, b.Samples[0].Failed)
	b = runBatch(context.Background(), c, self, "/missing/directory", "failed-config", []request{r}, 1)
	assert.True(t, b.Samples[0].Failed)
	c.Timeout = time.Nanosecond
	b = runBatch(context.Background(), c, self, t.TempDir(), "timeout", []request{r}, 1)
	assert.True(t, b.Samples[0].Failed)
}

func TestCampaign(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	c, err := parse(baseArgs(self))
	require.NoError(t, err)
	c.Encoders = []int{1, 4}
	c.Workers = []int{1, 2}
	c.Budget = 1
	r, err := campaign(context.Background(), c)
	require.NoError(t, err)
	assert.Len(t, r.Batches, 2)
	assert.Len(t, r.Omitted, 3)
	for _, b := range r.Batches {
		for _, o := range b.Samples {
			assert.False(t, o.Failed, o.Error)
		}
	}
	var output bytes.Buffer
	c.Inputs = []string{self}
	require.NoError(t, run(context.Background(), baseArgs(self), strings.NewReader(""), &output))
	require.True(t, json.Valid(output.Bytes()))
	require.Error(t, run(context.Background(), nil, strings.NewReader(""), &output))
	c.Inputs = []string{missingPath}
	_, err = campaign(context.Background(), c)
	require.Error(t, err)
	c.Inputs = []string{self}
	c.Dictionary = missingPath
	_, err = campaign(context.Background(), c)
	require.Error(t, err)
}

func TestDictionaryAndReadinessCampaign(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	c, err := parse(baseArgs(self))
	require.NoError(t, err)
	sample := bytes.Repeat([]byte("binary dictionary patterns and bytes"), 100)
	dict, err := codec.TrainDictionary([][]byte{sample, append([]byte("prefix"), sample...)}, 512, "default")
	require.NoError(t, err)
	c.Dictionary = writeTest(t, dict)
	c.Execute, c.PayloadArgs, c.ReadyOutput = true, []string{"_ready"}, "READY\n"
	r, err := campaign(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, digest(dict), r.DictionarySHA256)
	for _, b := range r.Batches {
		for _, o := range b.Samples {
			assert.False(t, o.Failed, o.Error)
		}
	}
	assert.Equal(t, "temporary-file-pipeline", r.Batches[1].Samples[0].State)
	assert.Positive(t, r.Batches[1].Samples[0].ApplicationNS)
}

func TestCampaignFailures(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	c, err := parse(baseArgs(self))
	require.NoError(t, err)
	c.Level = "invalid"
	_, err = campaign(context.Background(), c)
	require.Error(t, err)
	require.Error(t, run(context.Background(), baseArgs(missingPath), strings.NewReader(""), io.Discard))
	c.Level = "default"
	c.Inputs = []string{missingPath}
	_, err = decoderBatches(context.Background(), c, self, t.TempDir(), []payload{{}})
	require.Error(t, err)
	c.Inputs = []string{self}
	c.Dictionary = missingPath
	_, err = decoderBatches(context.Background(), c, self, t.TempDir(), []payload{{}})
	require.Error(t, err)
	c.Dictionary = ""
	_, err = decoderBatches(context.Background(), c, self, "/missing/directory", []payload{{}})
	require.Error(t, err)
	// Repeated real native inputs must still obey the aggregate admission bound.
	c.Inputs = make([]string, maxInputs)
	for i := range c.Inputs {
		c.Inputs[i] = self
	}
	_, err = campaign(context.Background(), c)
	// A small non-race test executable may fit; a rejected corpus must say why.
	if err != nil {
		require.ErrorContains(t, err, "64MiB")
	}
	h := observeHostSources([]string{missingPath}, func() (cgroup.Limits, error) { return cgroup.Limits{}, errors.New("unavailable limits") })
	assert.Contains(t, h.LimitsError, "unavailable")
	assert.Contains(t, h.Observations[missingPath], "unavailable")
}

func TestEntrypointAndTemporaryFailure(t *testing.T) {
	savedArgs := os.Args
	t.Cleanup(func() { os.Args = savedArgs })
	os.Args = []string{"research375"}
	assert.Equal(t, 1, entry())
	t.Setenv("TMPDIR", "/missing/temporary-directory")
	_, err := campaign(context.Background(), config{})
	require.Error(t, err)
	require.Error(t, execute(context.Background(), []byte("bytes"), request{}))
}
