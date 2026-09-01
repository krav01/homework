//go:build linux || darwin

package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lockStore(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open store lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			err = ErrStoreLocked
		}
		return nil, errors.Join(fmt.Errorf("lock store: %w", err), file.Close())
	}
	return file, nil
}
