package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInvalidInvocation(t *testing.T) {
	oldArgs, oldExit := os.Args, exit
	t.Cleanup(func() { os.Args, exit = oldArgs, oldExit })
	os.Args = []string{"bench-server", "-invalid"}
	code := 0
	exit = func(value int) { code = value }
	main()
	assert.Equal(t, 1, code)
}
