package store

import (
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileStore_AddAndReload(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "data", "sunday.json")
	groceries, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	tests := []struct {
		name    string
		user    string
		product string
		amount  int64
		want    int64
	}{
		{name: "first user", user: "loki", product: "apple", amount: 1, want: 1},
		{name: "second user same product", user: "thor", product: "apple", amount: 3, want: 4},
		{name: "existing user", user: "loki", product: "apple", amount: 2, want: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, addErr := groceries.Add(test.user, test.product, test.amount)
			if addErr != nil {
				t.Fatalf("Add() error = %v", addErr)
			}
			if got != test.want {
				t.Fatalf("Add() = %d, want %d", got, test.want)
			}
		})
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload NewFileStore() error = %v", err)
	}
	if got := reloaded.ProductAmount("apple"); got != 6 {
		t.Fatalf("ProductAmount() = %d, want 6", got)
	}
}

func TestFileStore_DeleteProduct(t *testing.T) {
	t.Parallel()

	groceries, err := NewFileStore(filepath.Join(t.TempDir(), "sunday.json"))
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if _, err := groceries.Add("loki", "coffee", 2); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := groceries.Add("thor", "coffee", 4); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	deleted, err := groceries.DeleteProduct("coffee")
	if err != nil {
		t.Fatalf("DeleteProduct() error = %v", err)
	}
	if !deleted {
		t.Fatal("DeleteProduct() = false, want true")
	}
	if got := groceries.ProductAmount("coffee"); got != 0 {
		t.Fatalf("ProductAmount() = %d, want 0", got)
	}

	deleted, err = groceries.DeleteProduct("coffee")
	if err != nil {
		t.Fatalf("second DeleteProduct() error = %v", err)
	}
	if deleted {
		t.Fatal("second DeleteProduct() = true, want false")
	}
}

func TestFileStore_ConcurrentAdd(t *testing.T) {
	t.Parallel()

	groceries, err := NewFileStore(filepath.Join(t.TempDir(), "sunday.json"))
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	const workers = 20
	var waitGroup sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, addErr := groceries.Add("loki", "yogurt", 1)
			errorsChannel <- addErr
		}()
	}
	waitGroup.Wait()
	close(errorsChannel)
	for addErr := range errorsChannel {
		if addErr != nil {
			t.Fatalf("Add() error = %v", addErr)
		}
	}
	if got := groceries.ProductAmount("yogurt"); got != workers {
		t.Fatalf("ProductAmount() = %d, want %d", got, workers)
	}
}

func TestFileStore_AddOverflow(t *testing.T) {
	t.Parallel()

	groceries, err := NewFileStore(filepath.Join(t.TempDir(), "sunday.json"))
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if _, err := groceries.Add("loki", "coffee", math.MaxInt64); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := groceries.Add("thor", "coffee", 1); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("Add() error = %v, want ErrAmountOverflow", err)
	}
}
