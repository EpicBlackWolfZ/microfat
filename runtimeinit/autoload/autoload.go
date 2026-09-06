// Package autoload automatically applies container cgroup resource auto-tuning at process startup.
//
// To automatically configure GOMEMLIMIT, GOMAXPROCS, and GOGC based on detected container cgroup limits,
// add a blank import to your main package or application entrypoint:
//
//	import _ "github.com/EpicBlackWolfZ/microfat/runtimeinit/autoload"
//
// For custom options or programmatic control, import "github.com/EpicBlackWolfZ/microfat/runtimeinit" directly
// and invoke runtimeinit.AutoTune(opts...).
package autoload

import (
	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
)

func init() {
	_ = runtimeinit.AutoTune()
}
