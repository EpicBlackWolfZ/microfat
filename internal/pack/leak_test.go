package pack

import (
	"os"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.CheckLeaksIfEnabled(m))
}
