// Package store persists Sunday groceries data.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

var ErrAmountOverflow = errors.New("product amount overflow")

type state map[string]map[string]int64

// FileStore atomically persists groceries to one JSON file.
type FileStore struct {
	mu   sync.RWMutex
	path string
	data state
}

// NewFileStore opens path or creates an empty store when it does not exist.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("store path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}

	loaded, err := load(path)
	if err != nil {
		return nil, err
	}
	return &FileStore{path: path, data: loaded}, nil
}

// Add increments the amount of product assigned by a user and returns its total.
func (s *FileStore) Add(user, product string, amount int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	if err := persist(s.path, next); err != nil {
		return 0, err
	}
	s.data = next
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
	if err := persist(s.path, next); err != nil {
		return false, err
	}
	s.data = next
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
	if len(bytes.TrimSpace(contents)) == 0 {
		return make(state), nil
	}
	var data state
	if err := json.Unmarshal(contents, &data); err != nil {
		return nil, fmt.Errorf("decode store: %w", err)
	}
	if data == nil {
		data = make(state)
	}
	return data, nil
}

func persist(path string, data state) (returnErr error) {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".sunday-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary store: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryName); err != nil && !errors.Is(err, os.ErrNotExist) && returnErr == nil {
			returnErr = fmt.Errorf("remove temporary store: %w", err)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("set store permissions: %w", err), closeFile(temporary))
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return errors.Join(fmt.Errorf("encode store: %w", err), closeFile(temporary))
	}
	if err := temporary.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync store: %w", err), closeFile(temporary))
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace store: %w", err)
	}

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
