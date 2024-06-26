//go:build !(darwin || linux || freebsd || windows)

package platform

import (
	"fmt"
	"runtime"
)

var errUnsupported = fmt.Errorf("mmap unsupported on GOOS=%s. Use interpreter instead.", runtime.GOOS)

const MmapSupported = false

func munmapCodeSegment(code []byte) error {
	panic(errUnsupported)
}

func mmapCodeSegmentAMD64(size int) ([]byte, error) {
	panic(errUnsupported)
}

func mmapCodeSegmentARM64(size int) ([]byte, error) {
	panic(errUnsupported)
}

func mmapMemory(size int) ([]byte, error) {
	panic(errUnsupported)
}

func MprotectRX(b []byte) (err error) {
	panic(errUnsupported)
}
