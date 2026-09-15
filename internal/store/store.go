// Package store persists Sunday groceries data.
package store

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

const persistedStateVersion = 1

var (
	ErrAmountOverflow       = errors.New("product amount overflow")
	ErrIdempotencyConflict  = errors.New("idempotency key was already used with a different request")
	ErrStoreLocked          = errors.New("store is already open by another writer")
	ErrStoreClosed          = errors.New("store is closed")
	ErrDurabilityUnknown    = errors.New("store durability is uncertain; reopen before writing")
)

type groceriesState map[string]map[string]int64

type idempotencyRecord struct {
	Fingerprint string `json:"fingerprint"`
	Total       int64  `json:"total"`
}

type state struct {
	Groceries   groceriesState
	Idempotency map[string]idempotencyRecord
}

type persistedState struct {
	FormatVersion int                          `json:"__format_version"`
	Groceries     groceriesState               `json:"groceries"`
	Idempotency   map[string]idempotencyRecord `json:"idempotency,omitempty"`
}

// FileStore atomically persists groceries and idempotency records to one JSON file.
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

// Ready reports whether the store can safely accept writes. Reads can still be
// available after a durability failure, but the HTTP service must stop receiving
// traffic until an operator reopens the store and re-establishes a known state.
func (s *FileStore) Ready() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lock == nil {
		return ErrStoreClosed
	}
	return s.writeErr
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
	total, _, err := s.add(user, product, amount, "")
	return total, err
}

// AddIdempotent performs Add once for a stable idempotency key. Replays of the
// same request return the original total without mutating state. Reusing a key
// for a different request returns ErrIdempotencyConflict.
func (s *FileStore) AddIdempotent(user, product string, amount int64, key string) (total int64, replayed bool, err error) {
	return s.add(user, product, amount, key)
}

func (s *FileStore) add(user, product string, amount int64, key string) (total int64, replayed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return 0, false, s.writeErr
	}
	if amount <= 0 {
		return 0, false, errors.New("amount must be positive")
	}

	var recordKey, fingerprint string
	if key != "" {
		recordKey = digest(key)
		fingerprint = requestFingerprint(user, product, amount)
		if record, ok := s.data.Idempotency[recordKey]; ok {
			if record.Fingerprint != fingerprint {
				return 0, false, ErrIdempotencyConflict
			}
			return record.Total, true, nil
		}
	}

	next := cloneState(s.data)
	if next.Groceries[user] == nil {
		next.Groceries[user] = make(map[string]int64)
	}
	currentTotal := productAmount(next, product)
	if amount > math.MaxInt64-currentTotal {
		return 0, false, ErrAmountOverflow
	}
	if amount > math.MaxInt64-next.Groceries[user][product] {
		return 0, false, ErrAmountOverflow
	}
	next.Groceries[user][product] += amount
	total = currentTotal + amount
	if key != "" {
		next.Idempotency[recordKey] = idempotencyRecord{Fingerprint: fingerprint, Total: total}
	}
	if err := s.commit(next); err != nil {
		return 0, false, err
	}
	return total, false, nil
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
	for user, products := range next.Groceries {
		if _, ok := products[product]; ok {
			delete(products, product)
			found = true
		}
		if len(products) == 0 {
			delete(next.Groceries, user)
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
		return emptyState(), nil
	}
	if err != nil {
		return state{}, fmt.Errorf("read store: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(contents, &raw); err != nil {
		return state{}, fmt.Errorf("decode store: %w", err)
	}
	if raw == nil {
		return state{}, errors.New("store must contain a JSON object")
	}

	var loaded state
	if _, versioned := raw["__format_version"]; versioned {
		var persisted persistedState
		if err := json.Unmarshal(contents, &persisted); err != nil {
			return state{}, fmt.Errorf("decode versioned store: %w", err)
		}
		if persisted.FormatVersion != persistedStateVersion {
			return state{}, fmt.Errorf("unsupported store format version %d", persisted.FormatVersion)
		}
		if persisted.Groceries == nil {
			return state{}, errors.New("store groceries must contain a JSON object")
		}
		if persisted.Idempotency == nil {
			persisted.Idempotency = make(map[string]idempotencyRecord)
		}
		loaded = state{Groceries: persisted.Groceries, Idempotency: persisted.Idempotency}
	} else {
		// Backward compatibility with the original on-disk format, where the
		// top-level object was the groceries map itself.
		var groceries groceriesState
		if err := json.Unmarshal(contents, &groceries); err != nil {
			return state{}, fmt.Errorf("decode legacy store: %w", err)
		}
		if groceries == nil {
			return state{}, errors.New("store must contain a JSON object")
		}
		loaded = state{Groceries: groceries, Idempotency: make(map[string]idempotencyRecord)}
	}
	if err := validateState(loaded); err != nil {
		return state{}, err
	}
	return loaded, nil
}

func validateState(data state) error {
	totals := make(map[string]int64)
	for _, products := range data.Groceries {
		for product, amount := range products {
			if amount <= 0 || amount > math.MaxInt64-totals[product] {
				return errors.New("invalid stored product amount")
			}
			totals[product] += amount
		}
	}
	for key, record := range data.Idempotency {
		if !validDigest(key) || !validDigest(record.Fingerprint) || record.Total <= 0 {
			return errors.New("invalid stored idempotency record")
		}
	}
	return nil
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
	if err := encoder.Encode(persistedState{
		FormatVersion: persistedStateVersion,
		Groceries:     data.Groceries,
		Idempotency:   data.Idempotency,
	}); err != nil {
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

func emptyState() state {
	return state{
		Groceries:   make(groceriesState),
		Idempotency: make(map[string]idempotencyRecord),
	}
}

func cloneState(input state) state {
	result := emptyState()
	for user, products := range input.Groceries {
		result.Groceries[user] = make(map[string]int64, len(products))
		for product, amount := range products {
			result.Groceries[user][product] = amount
		}
	}
	for key, record := range input.Idempotency {
		result.Idempotency[key] = record
	}
	return result
}

func productAmount(data state, product string) int64 {
	var total int64
	for _, products := range data.Groceries {
		total += products[product]
	}
	return total
}

func requestFingerprint(user, product string, amount int64) string {
	return digest(fmt.Sprintf("%s\x00%s\x00%d", user, product, amount))
}

func digest(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
