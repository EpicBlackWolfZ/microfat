//go:build !amd64

package microarch

func cpuid(_, _ uint32) (eax, ebx, ecx, edx uint32) {
	return 0, 0, 0, 0
}
