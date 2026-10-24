// Command prometheus demonstrates runtimeinfo with the Prometheus client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const serverTimeout = 5 * time.Second

type collector struct {
	read               func() runtimeinfo.Snapshot
	launcherInfo       *prometheus.Desc
	gomaxprocs         *prometheus.Desc
	gomemlimit         *prometheus.Desc
	memoryLimitEnabled *prometheus.Desc
	gogc               *prometheus.Desc
	gogcEnabled        *prometheus.Desc
	cgroupCPUQuota     *prometheus.Desc
	cgroupMemoryLimit  *prometheus.Desc
}

func newCollector() *collector {
	return &collector{
		read: runtimeinfo.Read,
		launcherInfo: prometheus.NewDesc("microfat_launcher_hint_info",
			"Informational launcher environment hints; not authenticated identity.",
			[]string{"selected_variant", "host_arch", "host_level", "exec_mode"}, nil),
		gomaxprocs: prometheus.NewDesc("microfat_runtime_gomaxprocs",
			"GOMAXPROCS observed from the running Go process.", nil, nil),
		gomemlimit: prometheus.NewDesc("microfat_runtime_gomemlimit_bytes",
			"Go process soft memory limit in bytes; omitted when disabled.", nil, nil),
		memoryLimitEnabled: prometheus.NewDesc("microfat_runtime_memory_limit_enabled",
			"Whether the observed Go process soft memory limit is enabled (1 or 0).", nil, nil),
		gogc: prometheus.NewDesc("microfat_runtime_gogc_percent",
			"Go process GC target percentage; omitted when the percentage-based trigger is disabled.", nil, nil),
		gogcEnabled: prometheus.NewDesc("microfat_runtime_gogc_enabled",
			"Whether the observed GOGC percentage-based GC trigger is enabled (1 or 0); memory-limit GC is independent.", nil, nil),
		cgroupCPUQuota: prometheus.NewDesc("microfat_launcher_cgroup_cpu_quota_hint",
			"Launcher environment hint for cgroup CPU quota in CPU equivalents; not a live cgroup observation.", nil, nil),
		cgroupMemoryLimit: prometheus.NewDesc("microfat_launcher_cgroup_memory_limit_hint_bytes",
			"Launcher memory limit hint in bytes; zero is ambiguous and this is not a live cgroup observation.", nil, nil),
	}
}

func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{
		c.launcherInfo, c.gomaxprocs, c.gomemlimit, c.memoryLimitEnabled,
		c.gogc, c.gogcEnabled, c.cgroupCPUQuota, c.cgroupMemoryLimit,
	} {
		ch <- desc
	}
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {
	snapshot := c.read()
	hints := snapshot.Launcher
	if hints.SelectedVariant.Status == runtimeinfo.StatusPresent {
		ch <- prometheus.MustNewConstMetric(c.launcherInfo, prometheus.GaugeValue, 1,
			hints.SelectedVariant.Value, presentString(hints.HostArch),
			presentString(hints.HostLevel), presentString(hints.ExecMode))
	}

	observed := snapshot.Observed
	if observed.GOMAXPROCS.Status == runtimeinfo.StatusPresent {
		ch <- prometheus.MustNewConstMetric(c.gomaxprocs, prometheus.GaugeValue, float64(observed.GOMAXPROCS.Value))
	}
	if observed.GOMEMLIMITBytes.Status == runtimeinfo.StatusPresent {
		enabled := observed.GOMEMLIMITBytes.Value != math.MaxInt64
		ch <- prometheus.MustNewConstMetric(c.memoryLimitEnabled, prometheus.GaugeValue, boolFloat(enabled))
		if enabled {
			ch <- prometheus.MustNewConstMetric(c.gomemlimit, prometheus.GaugeValue, float64(observed.GOMEMLIMITBytes.Value))
		}
	}
	if observed.GOGCPercent.Status == runtimeinfo.StatusPresent {
		enabled := observed.GOGCPercent.Value != math.MaxUint64
		ch <- prometheus.MustNewConstMetric(c.gogcEnabled, prometheus.GaugeValue, boolFloat(enabled))
		if enabled {
			ch <- prometheus.MustNewConstMetric(c.gogc, prometheus.GaugeValue, float64(observed.GOGCPercent.Value))
		}
	}
	if hints.Cgroup.CPUQuota.Status == runtimeinfo.StatusPresent {
		ch <- prometheus.MustNewConstMetric(c.cgroupCPUQuota, prometheus.GaugeValue, hints.Cgroup.CPUQuota.Value)
	}
	if hints.Cgroup.MemoryLimitBytes.Status == runtimeinfo.StatusPresent {
		ch <- prometheus.MustNewConstMetric(c.cgroupMemoryLimit, prometheus.GaugeValue, float64(hints.Cgroup.MemoryLimitBytes.Value))
	}
}

func presentString(field runtimeinfo.Field[string]) string {
	if field.Status == runtimeinfo.StatusPresent {
		return field.Value
	}
	return ""
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func main() {
	once := flag.Bool("once", false, "print one scrape and exit without opening a socket")
	listen := flag.String("listen", "127.0.0.1:9090", "HTTP listen address")
	flag.Parse()
	if err := run(*once, *listen); err != nil {
		log.Fatal(err)
	}
}

func run(once bool, listen string) error {
	registry := prometheus.NewRegistry()
	if err := registry.Register(newCollector()); err != nil {
		return fmt.Errorf("register runtime collector: %w", err)
	}
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	if once {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if response.Code != http.StatusOK {
			return fmt.Errorf("collect metrics: HTTP status %d", response.Code)
		}
		if _, err := io.Copy(os.Stdout, response.Body); err != nil {
			return fmt.Errorf("write metrics: %w", err)
		}
		return nil
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", handler)
	server := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: serverTimeout}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	log.Printf("serving metrics at http://%s/metrics", listen)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down metrics server: %w", err)
		}
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve metrics: %w", err)
		}
	}
	return nil
}
