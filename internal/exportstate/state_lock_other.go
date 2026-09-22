//go:build !unix

package exportstate

import (
	"fmt"
	"os"
	"runtime"
)

func tryStateLock(_ *os.File) error {
	return fmt.Errorf("export state locking is unsupported on %s", runtime.GOOS)
}
