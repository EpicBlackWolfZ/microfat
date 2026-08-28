//go:build amd64

package microarch

func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)
