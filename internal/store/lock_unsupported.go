//go:build !linux && !darwin

package store

import (
	"errors"
	"os"
)

func lockStore(string) (*os.File, error) {
	return nil, errors.New("file store requires Linux or macOS advisory file locking")
}
