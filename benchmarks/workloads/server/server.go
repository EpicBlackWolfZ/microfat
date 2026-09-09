// Package server provides identical deterministic workloads for native and fat executables.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/bits"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
)

const (
	byteMask           = 0xff
	DefaultPayload     = 4096
	DefaultIterations  = 4
	DefaultConcurrency = 16
	MaxPayload         = 1024 * 1024
	MaxIterations      = 128
	MaxConcurrency     = 128
	headerTimeout      = 5 * time.Second
	shutdownTimeout    = 5 * time.Second
	requestTimeout     = 30 * time.Second
	matrixWidth        = 16
	rotation           = 13
	graphStride        = 64
)

// Level is set at build time for every ISA payload, including the generic baseline.
var Level = "unknown"

type Config struct {
	Address      string
	PayloadBytes int
	Iterations   int
	Concurrency  int
	Seed         uint64
}

func Defaults() Config {
	return Config{Address: "127.0.0.1:0", PayloadBytes: DefaultPayload, Iterations: DefaultIterations,
		Concurrency: DefaultConcurrency, Seed: 1}
}

func (c Config) Validate() error {
	host, _, err := net.SplitHostPort(c.Address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("benchmark server address must be a numeric loopback address and port")
	}
	if c.PayloadBytes <= 0 || c.PayloadBytes > MaxPayload || c.Iterations <= 0 || c.Iterations > MaxIterations ||
		c.Concurrency <= 0 || c.Concurrency > MaxConcurrency {
		return errors.New("workload intensity exceeds bounds")
	}
	return nil
}

type reply struct {
	Checksum string `json:"checksum"`
	Value    uint64 `json:"value"`
}

type mixedInput struct {
	Data string `json:"data"`
	Seed uint64 `json:"seed"`
}

func Handler(cfg Config) (http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /diagnostics", diagnostics)
	slots := make(chan struct{}, cfg.Concurrency)
	for _, name := range []string{"mixed", "cpu", "memory"} {
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				http.Error(w, "concurrency limit", http.StatusServiceUnavailable)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			result, err := execute(r, cfg, name)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.MarshalWrite(w, result); err != nil {
				return
			}
		})
	}
	return mux, nil
}

func execute(r *http.Request, cfg Config, name string) (reply, error) {
	if err := r.Context().Err(); err != nil {
		return reply{}, err
	}
	if name == "mixed" {
		return mixed(r, cfg)
	}
	var value uint64
	for i := range cfg.Iterations {
		if err := r.Context().Err(); err != nil {
			return reply{}, err
		}
		if name == "cpu" {
			value ^= cpu(cfg.Seed + uint64(i))
		} else {
			value ^= memory(cfg, uint64(i))
		}
	}
	return reply{Value: value}, nil
}

func mixed(r *http.Request, cfg Config) (reply, error) {
	input := mixedInput{Data: strings.Repeat("x", cfg.PayloadBytes), Seed: cfg.Seed}
	if r.Body != nil && r.Body != http.NoBody {
		data, err := io.ReadAll(io.LimitReader(r.Body, int64(cfg.PayloadBytes)+1))
		if err != nil {
			return reply{}, err
		}
		if len(data) > cfg.PayloadBytes {
			return reply{}, errors.New("request body exceeds configured payload limit")
		}
		if err := json.Unmarshal(data, &input); err != nil {
			return reply{}, err
		}
	}
	buf := []byte(input.Data)
	var digest [sha256.Size]byte
	for i := range cfg.Iterations {
		if err := r.Context().Err(); err != nil {
			return reply{}, err
		}
		for j := range buf {
			buf[j] ^= byte((input.Seed + uint64(i)) & byteMask)
		}
		digest = sha256.Sum256(buf)
	}
	return reply{Checksum: hex.EncodeToString(digest[:]), Value: input.Seed}, nil
}

func cpu(seed uint64) uint64 {
	var a, b [matrixWidth][matrixWidth]uint64
	for i := range matrixWidth {
		for j := range matrixWidth {
			a[i][j] = bits.RotateLeft64(seed+uint64(i*matrixWidth+j), rotation)
			b[i][j] = seed ^ uint64(i+j+1)
		}
	}
	var sum uint64
	for i := range matrixWidth {
		for j := range matrixWidth {
			var value uint64
			for k := range matrixWidth {
				value += a[i][k] * b[k][j]
			}
			sum ^= bits.RotateLeft64(value, j)
		}
	}
	return sum
}

type node struct {
	next *node
	data []byte
}

func memory(cfg Config, iteration uint64) uint64 {
	var head *node
	for i := 0; i < cfg.PayloadBytes; i += graphStride {
		data := make([]byte, 0, 1)
		for j := range graphStride {
			data = append(data, byte((uint64(i+j)+cfg.Seed+iteration)&byteMask))
		}
		head = &node{next: head, data: data}
	}
	var sum uint64
	for current := head; current != nil; current = current.next {
		for _, v := range current.data {
			sum += uint64(v)
		}
	}
	runtime.KeepAlive(head)
	return sum
}

func diagnostics(w http.ResponseWriter, _ *http.Request) {
	info := map[string]string{"level": Level, "go_version": runtime.Version(), "gomaxprocs": strconv.Itoa(runtime.GOMAXPROCS(0)),
		"gomemlimit": strconv.FormatInt(debug.SetMemoryLimit(-1), 10), "pid": strconv.Itoa(os.Getpid())}
	samples := []metrics.Sample{{Name: "/gc/gogc:percent"}}
	metrics.Read(samples)
	info["gogc"] = strconv.FormatUint(samples[0].Value.Uint64(), 10)
	for _, key := range []string{"MICROFAT_EXEC_MODE", "MICROFAT_SELECTED_VARIANT", "MICROFAT_SELECTED_SHA256"} {
		info[key] = os.Getenv(key)
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		info["build_info"] = build.String()
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.MarshalWrite(w, info, json.Deterministic(true)); err != nil {
		return
	}
}

func Serve(ctx context.Context, cfg Config, ready io.Writer) error {
	handler, err := Handler(cfg)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: headerTimeout, ReadTimeout: requestTimeout,
		WriteTimeout: requestTimeout, IdleTimeout: requestTimeout}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if err := json.MarshalWrite(ready, map[string]string{"url": "http://" + listener.Addr().String()}); err != nil {
		return errors.Join(err, server.Close(), <-done)
	}
	if _, err := fmt.Fprintln(ready); err != nil {
		return errors.Join(err, server.Close(), <-done)
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		serveErr := <-done
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, serveErr)
	}
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg := Defaults()
	flags := flag.NewFlagSet("bench-server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.Address, "listen", cfg.Address, "Loopback address")
	flags.IntVar(&cfg.PayloadBytes, "payload", cfg.PayloadBytes, "Payload bytes")
	flags.IntVar(&cfg.Iterations, "iterations", cfg.Iterations, "Work per request")
	flags.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "Maximum in-flight requests")
	flags.Uint64Var(&cfg.Seed, "seed", cfg.Seed, "Deterministic workload seed")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	runtimeinit.AutoTune()
	return Serve(ctx, cfg, stdout)
}
