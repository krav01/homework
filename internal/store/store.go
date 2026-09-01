// Package store persists Sunday groceries data.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrAmountOverflow    = errors.New("product amount overflow")
	ErrStoreLocked       = errors.New("store is already open by another writer")
	ErrStoreClosed       = errors.New("store is closed")
	ErrDurabilityUnknown = errors.New("store durability is uncertain; reopen before writing")
)

type state map[string]map[string]int64

// FileStore atomically persists groceries to one JSON file.
type FileStore struct {
	mu       sync.RWMutex
	path     string
	data     state
	lock     *os.File
	writeErr error
	persist  func(string, state) (bool, error)
}

// NewFileStore opens path or creates an empty store when it does not exist.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("store path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}

	lock, err := lockStore(path + ".lock")
	if err != nil {
		return nil, err
	}
	loaded, err := load(path)
	if err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return &FileStore{path: path, data: loaded, lock: lock, persist: persist}, nil
}

// Close releases the lifetime writer lock after all requests have stopped.
// The sidecar lock file is retained: unlinking it would let writers lock different inodes.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	s.writeErr = ErrStoreClosed
	if err != nil {
		return fmt.Errorf("close store lock: %w", err)
	}
	return nil
}

// commit keeps memory consistent with a successful rename even if the directory
// sync fails. Further writes are rejected until the store is reopened.
func (s *FileStore) commit(next state) error {
	replaced, err := s.persist(s.path, next)
	if replaced {
		s.data = next
	}
	if err != nil && replaced {
		s.writeErr = errors.Join(ErrDurabilityUnknown, err)
		return s.writeErr
	}
	return err
}

// Add increments the amount of product assigned by a user and returns its total.
func (s *FileStore) Add(user, product string, amount int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	if amount <= 0 {
		return 0, errors.New("amount must be positive")
	}

	next := cloneState(s.data)
	if next[user] == nil {
		next[user] = make(map[string]int64)
	}
	currentTotal := productAmount(next, product)
	if amount > math.MaxInt64-currentTotal {
		return 0, ErrAmountOverflow
	}
	if amount > math.MaxInt64-next[user][product] {
		return 0, ErrAmountOverflow
	}
	next[user][product] += amount
	if err := s.commit(next); err != nil {
		return 0, err
	}
	return currentTotal + amount, nil
}

// ProductAmount returns the sum of a product across all users.
func (s *FileStore) ProductAmount(product string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return productAmount(s.data, product)
}

// DeleteProduct removes a product from every user and reports whether it existed.
func (s *FileStore) DeleteProduct(product string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return false, s.writeErr
	}

	next := cloneState(s.data)
	found := false
	for user, products := range next {
		if _, ok := products[product]; ok {
			delete(products, product)
			found = true
		}
		if len(products) == 0 {
			delete(next, user)
		}
	}
	if !found {
		return false, nil
	}
	if err := s.commit(next); err != nil {
		return false, err
	}
	return true, nil
}

func load(path string) (state, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(state), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read store: %w", err)
	}
	var data state
	if err := json.Unmarshal(contents, &data); err != nil {
		return nil, fmt.Errorf("decode store: %w", err)
	}
	if data == nil {
		return nil, errors.New("store must contain a JSON object")
	}
	totals := make(map[string]int64)
	for _, products := range data {
		for product, amount := range products {
			if amount <= 0 || amount > math.MaxInt64-totals[product] {
				return nil, errors.New("invalid stored product amount")
			}
			totals[product] += amount
		}
	}
	return data, nil
}

func persist(path string, data state) (bool, error) {
	return persistWithOps(path, data, os.Rename, syncDirectory)
}

// The injected operations let tests exercise errors on both sides of the commit point.
func persistWithOps(path string, data state, rename func(string, string) error, syncDir func(string) error) (replaced bool, returnErr error) {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".sunday-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temporary store: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryName); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary store: %w", err))
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return false, errors.Join(fmt.Errorf("set store permissions: %w", err), closeFile(temporary))
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return false, errors.Join(fmt.Errorf("encode store: %w", err), closeFile(temporary))
	}
	if err := temporary.Sync(); err != nil {
		return false, errors.Join(fmt.Errorf("sync store: %w", err), closeFile(temporary))
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close store: %w", err)
	}
	if err := rename(temporaryName, path); err != nil {
		return false, fmt.Errorf("replace store: %w", err)
	}
	return true, syncDir(dir)
}

func syncDirectory(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open store directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		syncErr = fmt.Errorf("sync store directory: %w", syncErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close store directory: %w", closeErr)
	}
	return errors.Join(syncErr, closeErr)
}

func closeFile(file *os.File) error {
	if err := file.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

func cloneState(input state) state {
	result := make(state, len(input))
	for user, products := range input {
		result[user] = make(map[string]int64, len(products))
		for product, amount := range products {
			result[user][product] = amount
		}
	}
	return result
}

func productAmount(data state, product string) int64 {
	var total int64
	for _, products := range data {
		total += products[product]
	}
	return total
}
