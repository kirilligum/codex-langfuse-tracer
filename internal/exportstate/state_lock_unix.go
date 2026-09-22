//go:build unix

package exportstate

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryStateLock(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrLockBusy
	}
	if errors.Is(err, unix.EINTR) {
		return errLockInterrupted
	}
	return err
}
