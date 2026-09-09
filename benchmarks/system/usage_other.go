//go:build !unix

package system

import (
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"os"
)

func Usage(_ *os.ProcessState) map[string]schema.Measurement {
	return map[string]schema.Measurement{"peak_rss_bytes": schema.Unavailable("bytes", "lifetime", "wait", "unsupported OS")}
}
