package main

import (
	"os"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/testutil"
)

const testArchAMD64 = "amd64"

func TestMain(m *testing.M) {
	os.Exit(testutil.CheckLeaksIfEnabled(m))
}
