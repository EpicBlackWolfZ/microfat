package main

import (
	"fmt"

	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
)

func main() {
	res := runtimeinit.AutoTune(runtimeinit.WithProfile(runtimeinit.ProfileLatencyCritical))
	fmt.Printf("profile=%s gogc=%d\n", res.ProfileApplied, res.GOGC)
}
