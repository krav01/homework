package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_AddIdempotentSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sunday.json")
	first, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	total, replayed, err := first.AddIdempotent("alice", "apple", 3, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || replayed {
		t.Fatalf("first write total=%d replayed=%v", total, replayed)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()

	total, replayed, err = reopened.AddIdempotent("alice", "apple", 3, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || !replayed {
		t.Fatalf("replay after reopen total=%d replayed=%v", total, replayed)
	}
	if got := reopened.ProductAmount("apple"); got != 3 {
		t.Fatalf("replay after reopen duplicated amount=%d", got)
	}
}

func TestFileStore_AddIdempotentRejectsKeyReuse(t *testing.T) {
	groceries, err := NewFileStore(filepath.Join(t.TempDir(), "sunday.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := groceries.Close(); err != nil {
			t.Error(err)
		}
	}()

	if _, _, err := groceries.AddIdempotent("alice", "apple", 2, "request-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := groceries.AddIdempotent("alice", "apple", 4, "request-1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reuse error=%v want ErrIdempotencyConflict", err)
	}
	if got := groceries.ProductAmount("apple"); got != 2 {
		t.Fatalf("conflicting replay mutated amount=%d", got)
	}
}

func TestFileStore_LoadsLegacyFormatAndMigratesOnWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sunday.json")
	if err := os.WriteFile(path, []byte(`{"alice":{"apple":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	groceries, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := groceries.ProductAmount("apple"); got != 2 {
		t.Fatalf("legacy amount=%d want=2", got)
	}
	if _, _, err := groceries.AddIdempotent("bob", "apple", 1, "migration-write"); err != nil {
		t.Fatal(err)
	}
	if err := groceries.Close(); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte(`"__format_version": 1`)) {
		t.Fatalf("store was not migrated: %s", contents)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	if got := reopened.ProductAmount("apple"); got != 3 {
		t.Fatalf("migrated amount=%d want=3", got)
	}
}
