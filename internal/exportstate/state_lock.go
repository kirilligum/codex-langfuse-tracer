package exportstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	stateLockTimeout = 2 * time.Second
	stateLockPoll    = 25 * time.Millisecond
)

var ErrLockBusy = errors.New("export state lock remained busy")

type LockBusyError struct {
	Path   string
	Waited time.Duration
}

func (err *LockBusyError) Error() string {
	return fmt.Sprintf("%v: path=%s waited=%s", ErrLockBusy, err.Path, err.Waited)
}

func (err *LockBusyError) Is(target error) bool {
	return target == ErrLockBusy
}

var errLockInterrupted = errors.New("export state lock attempt interrupted")

func acquireLock(ctx context.Context, path string) (*os.File, error) {
	if ctx == nil {
		return nil, errors.New("export state lock requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create export state directory for %s: %w", path, err)
	}
	lockPath := path + ".lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open export state lock %s: %w", lockPath, err)
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("set export state lock permissions %s: %w", lockPath, err), file.Close())
	}
	deadline := time.Now().Add(stateLockTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		err := tryStateLock(file)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, ErrLockBusy) && !errors.Is(err, errLockInterrupted) {
			return nil, errors.Join(fmt.Errorf("acquire export state lock %s: %w", lockPath, err), file.Close())
		}
		if errors.Is(err, ErrLockBusy) && !time.Now().Before(deadline) {
			busyErr := &LockBusyError{Path: lockPath, Waited: stateLockTimeout}
			if closeErr := file.Close(); closeErr != nil {
				return nil, fmt.Errorf("close export state lock after contention on %s: %w", lockPath, closeErr)
			}
			return nil, busyErr
		}

		wait := stateLockPoll
		if untilDeadline := time.Until(deadline); wait > untilDeadline {
			wait = untilDeadline
		}
		if errors.Is(err, errLockInterrupted) {
			wait = 0
		}
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-timer.C:
		}
	}
}
