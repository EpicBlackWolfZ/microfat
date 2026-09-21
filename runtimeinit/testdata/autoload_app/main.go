package main

import (
	"fmt"
	"runtime/debug"

	_ "github.com/EpicBlackWolfZ/microfat/runtimeinit/autoload"
)

func main() {
	fmt.Println("autoload ok")
	// Observe the setting applied during init, then restore it immediately.
	const normalGC = 100
	previous := debug.SetGCPercent(normalGC)
	debug.SetGCPercent(previous)
	fmt.Printf("gogc=%d\n", previous)
}
