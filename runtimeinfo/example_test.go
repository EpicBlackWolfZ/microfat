package runtimeinfo_test

import (
	"fmt"

	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
)

func ExampleRead() {
	snapshot := runtimeinfo.Read()
	if snapshot.Launcher.SelectedVariant.Status == runtimeinfo.StatusPresent {
		fmt.Printf("launcher variant hint: %s\n", snapshot.Launcher.SelectedVariant.Value)
	}
	if snapshot.Observed.GOMAXPROCS.Status == runtimeinfo.StatusPresent {
		fmt.Printf("observed GOMAXPROCS: %d\n", snapshot.Observed.GOMAXPROCS.Value)
	}
}
