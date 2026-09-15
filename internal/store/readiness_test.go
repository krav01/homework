package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_ReadinessTracksWriteSafety(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sunday.json")
	groceries, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := groceries.Ready(); err != nil {
		t.Fatalf("Ready() error = %v, want nil", err)
	}

	fault := errors.New("injected directory sync failure")
	groceries.persist = func(path string, next state) (bool, error) {
		return persistWithOps(path, next, os.Rename, func(string) error { return fault })
	}
	if _, err := groceries.Add("alice", "apple", 1); !errors.Is(err, ErrDurabilityUnknown) {
		t.Fatalf("Add() error = %v, want ErrDurabilityUnknown", err)
	}
	if err := groceries.Ready(); !errors.Is(err, ErrDurabilityUnknown) {
		t.Fatalf("Ready() error = %v, want ErrDurabilityUnknown", err)
	}

	if err := groceries.Close(); err != nil {
		t.Fatal(err)
	}
	if err := groceries.Ready(); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Ready() after close = %v, want ErrStoreClosed", err)
	}
}
