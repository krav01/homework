package store

import (
	"errors"
	"math"
	"os"
	"os/exec"
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
	t.Cleanup(func() {
		if err := groceries.Close(); err != nil {
			t.Error(err)
		}
	})

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

	if err := groceries.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload NewFileStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reloaded.Close(); err != nil {
			t.Error(err)
		}
	})
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
	t.Cleanup(func() {
		if err := groceries.Close(); err != nil {
			t.Error(err)
		}
	})
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
	t.Cleanup(func() {
		if err := groceries.Close(); err != nil {
			t.Error(err)
		}
	})

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
	t.Cleanup(func() {
		if err := groceries.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := groceries.Add("loki", "coffee", math.MaxInt64); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := groceries.Add("thor", "coffee", 1); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("Add() error = %v, want ErrAmountOverflow", err)
	}
}

func TestFileStore_ExclusiveProcessLock(t *testing.T) {
	if path := os.Getenv("SUNDAY_LOCK_TEST_PATH"); path != "" {
		second, err := NewFileStore(path)
		if second != nil {
			if closeErr := second.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		if !errors.Is(err, ErrStoreLocked) {
			t.Fatalf("second process error = %v, want ErrStoreLocked", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "sunday.json")
	first, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	})
	cmd := exec.Command(os.Args[0], "-test.run=^TestFileStore_ExclusiveProcessLock$")
	cmd.Env = append(os.Environ(), "SUNDAY_LOCK_TEST_PATH="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	if _, err := first.Add("alice", "apple", 1); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Add("alice", "apple", 1); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed write: %v", err)
	}
	next, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := next.Close(); err != nil {
			t.Error(err)
		}
	})
	if got := next.ProductAmount("apple"); got != 1 {
		t.Fatalf("handoff total=%d", got)
	}
}

func TestFileStore_PersistenceFailure(t *testing.T) {
	for _, test := range []struct {
		name        string
		afterRename bool
		delete      bool
	}{
		{name: "add before rename"}, {name: "add after rename", afterRename: true},
		{name: "delete before rename", delete: true}, {name: "delete after rename", afterRename: true, delete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sunday.json")
			s, err := NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := s.Add("alice", "apple", 1); err != nil {
				t.Fatal(err)
			}
			fault := errors.New("injected filesystem failure")
			s.persist = func(path string, next state) (bool, error) {
				if test.afterRename {
					return persistWithOps(path, next, os.Rename, func(string) error { return fault })
				}
				return persistWithOps(path, next, func(string, string) error { return fault }, syncDirectory)
			}
			if test.delete {
				_, err = s.DeleteProduct("apple")
			} else {
				_, err = s.Add("bob", "apple", 1)
			}
			if !errors.Is(err, fault) {
				t.Fatalf("write error=%v", err)
			}
			want := int64(1)
			if test.afterRename {
				want = 2
				if test.delete {
					want = 0
				}
			}
			disk, err := load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.ProductAmount("apple"); got != want {
				t.Fatalf("memory=%d want=%d", got, want)
			}
			if got := productAmount(disk, "apple"); got != want {
				t.Fatalf("disk=%d want=%d", got, want)
			}
			s.persist = persist
			_, err = s.Add("bob", "apple", 1)
			if test.afterRename {
				if !errors.Is(err, ErrDurabilityUnknown) {
					t.Fatalf("write after uncertain commit=%v", err)
				}
			} else if err != nil {
				t.Fatalf("retry after uncommitted failure=%v", err)
			}
		})
	}
}

func TestFileStore_RejectsDamagedData(t *testing.T) {
	for _, input := range []string{"", "null", "{", `{"alice":{"apple":-1}}`, `{"alice":{"apple":9223372036854775807},"bob":{"apple":1}}`} {
		t.Run(input, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sunday.json")
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := NewFileStore(path)
			if s != nil {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			}
			if err == nil {
				t.Fatal("accepted damaged data")
			}
			// A failed open must release its lock so an operator can repair and reopen.
			if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			repaired, err := NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := repaired.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
