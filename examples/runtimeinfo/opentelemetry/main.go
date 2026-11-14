// Command opentelemetry demonstrates one runtimeinfo collection to stdout.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const collectionTimeout = 5 * time.Second

const (
	launcherInfo = iota
	gomaxprocs
	gomemlimit
	memoryLimitEnabled
	gogc
	gogcEnabled
	cgroupCPUQuota
	cgroupMemoryLimit
)

func main() {
	timeout := flag.Duration("timeout", collectionTimeout, "deadline for one collection and stdout export")
	flag.Parse()
	if *timeout <= 0 {
		log.Fatal("timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	err := run(ctx)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) (result error) {
	exporter, err := stdoutmetric.New(stdoutmetric.WithPrettyPrint())
	if err != nil {
		return fmt.Errorf("create stdout exporter: %w", err)
	}
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), collectionTimeout)
		defer cancel()
		result = errors.Join(result, provider.Shutdown(cleanupCtx), exporter.Shutdown(cleanupCtx))
	}()

	instruments, err := newInstruments(provider.Meter("microfat.runtimeinfo.example"))
	if err != nil {
		return err
	}
	registration, err := provider.Meter("microfat.runtimeinfo.example").RegisterCallback(
		func(_ context.Context, observer metric.Observer) error {
			observe(observer, instruments, runtimeinfo.Read())
			return nil
		}, asObservables(instruments)...)
	if err != nil {
		return fmt.Errorf("register runtime callback: %w", err)
	}
	defer func() { result = errors.Join(result, registration.Unregister()) }()

	var data metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &data); err != nil {
		return fmt.Errorf("collect runtime metrics: %w", err)
	}
	if err := exporter.Export(ctx, &data); err != nil {
		return fmt.Errorf("export runtime metrics: %w", err)
	}
	return nil
}

func newInstruments(meter metric.Meter) ([]metric.Float64ObservableGauge, error) {
	definitions := []struct{ name, description, unit string }{
		{"microfat.launcher.hint.info", "Informational launcher environment hints; not authenticated identity.", "1"},
		{"microfat.runtime.gomaxprocs", "GOMAXPROCS observed from the running Go process.", "{thread}"},
		{"microfat.runtime.gomemlimit", "Go process soft memory limit; omitted when disabled.", "By"},
		{"microfat.runtime.memory_limit.enabled", "Whether the observed Go process soft memory limit is enabled (1 or 0).", "1"},
		{"microfat.runtime.gogc", "Go process GC target percentage; omitted when the percentage-based trigger is disabled.", "%"},
		{"microfat.runtime.gogc.enabled", "Whether the GOGC percentage-based trigger is enabled; memory-limit GC is independent.", "1"},
		{"microfat.launcher.cgroup.cpu_quota.hint", "Launcher quota hint in CPU equivalents; not a live cgroup observation.", "{cpu}"},
		{"microfat.launcher.cgroup.memory_limit.hint", "Launcher memory limit hint; zero is ambiguous; not a live observation.", "By"},
	}
	instruments := make([]metric.Float64ObservableGauge, 0, len(definitions))
	for _, definition := range definitions {
		instrument, err := meter.Float64ObservableGauge(definition.name,
			metric.WithDescription(definition.description), metric.WithUnit(definition.unit))
		if err != nil {
			return nil, fmt.Errorf("create %s gauge: %w", definition.name, err)
		}
		instruments = append(instruments, instrument)
	}
	return instruments, nil
}

func asObservables(instruments []metric.Float64ObservableGauge) []metric.Observable {
	observables := make([]metric.Observable, len(instruments))
	for index, instrument := range instruments {
		observables[index] = instrument
	}
	return observables
}

func observe(observer metric.Observer, instruments []metric.Float64ObservableGauge, snapshot runtimeinfo.Snapshot) {
	hints := snapshot.Launcher
	if hints.SelectedVariant.Status == runtimeinfo.StatusPresent {
		attributes := []attribute.KeyValue{attribute.String("selected_variant", hints.SelectedVariant.Value)}
		for key, field := range map[string]runtimeinfo.Field[string]{
			"host_arch": hints.HostArch, "host_level": hints.HostLevel, "exec_mode": hints.ExecMode,
		} {
			if field.Status == runtimeinfo.StatusPresent {
				attributes = append(attributes, attribute.String(key, field.Value))
			}
		}
		observer.ObserveFloat64(instruments[launcherInfo], 1, metric.WithAttributes(attributes...))
	}
	observed := snapshot.Observed
	if observed.GOMAXPROCS.Status == runtimeinfo.StatusPresent {
		observer.ObserveFloat64(instruments[gomaxprocs], float64(observed.GOMAXPROCS.Value))
	}
	if observed.GOMEMLIMITBytes.Status == runtimeinfo.StatusPresent {
		enabled := observed.GOMEMLIMITBytes.Value != math.MaxInt64
		observer.ObserveFloat64(instruments[memoryLimitEnabled], boolFloat(enabled))
		if enabled {
			observer.ObserveFloat64(instruments[gomemlimit], float64(observed.GOMEMLIMITBytes.Value))
		}
	}
	if observed.GOGCPercent.Status == runtimeinfo.StatusPresent {
		enabled := observed.GOGCPercent.Value != math.MaxUint64
		observer.ObserveFloat64(instruments[gogcEnabled], boolFloat(enabled))
		if enabled {
			observer.ObserveFloat64(instruments[gogc], float64(observed.GOGCPercent.Value))
		}
	}
	if hints.Cgroup.CPUQuota.Status == runtimeinfo.StatusPresent {
		observer.ObserveFloat64(instruments[cgroupCPUQuota], hints.Cgroup.CPUQuota.Value)
	}
	if hints.Cgroup.MemoryLimitBytes.Status == runtimeinfo.StatusPresent {
		observer.ObserveFloat64(instruments[cgroupMemoryLimit], float64(hints.Cgroup.MemoryLimitBytes.Value))
	}
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
