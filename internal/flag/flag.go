// Package flag provides typed configuration value parsing from environment
// variables and .env files. It wraps Go's flag.Value interface to enable
// a uniform registry of configuration flags loaded from the environment.
package flag

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/joho/godotenv"
)

// Value is an interface for typed configuration values.
// Compatible with the standard library's flag.Value interface.
type Value interface {
	String() string
	Set(string) error
}

// Registry holds a map of named configuration values.
type Registry struct {
	mu     sync.RWMutex
	values map[string]Value
}

// New creates a new empty registry.
func New() *Registry {
	return &Registry{
		values: make(map[string]Value),
	}
}

// Register adds a named value to the registry.
func (r *Registry) Register(name string, v Value) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[name] = v
}

// Value returns the value for a given name, or nil if not found.
func (r *Registry) Value(name string) Value {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.values[name]
}

// Names returns all registered value names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.values))
	for k := range r.values {
		names = append(names, k)
	}
	return names
}

// LoadEnv loads values from a .env file (if present) and then from
// environment variables. Environment variables override .env file values.
// Unknown environment variables are ignored.
func (r *Registry) LoadEnv() error {
	// Load .env file if present (optional)
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to load .env file: %w", err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for name, v := range r.values {
		if val, ok := os.LookupEnv(name); ok {
			if err := v.Set(strings.TrimSpace(val)); err != nil {
				return fmt.Errorf("invalid value for %s: %w", name, err)
			}
		}
	}

	return nil
}

// --- Typed value implementations ---

// StringValue implements Value for string configuration.
type StringValue struct {
	val string
	def string
}

// NewString creates a StringValue with the given default.
func NewString(def string) *StringValue {
	return &StringValue{val: def, def: def}
}

func (v *StringValue) String() string { return v.val }
func (v *StringValue) Set(s string) error {
	v.val = s
	return nil
}

// Get returns the current value.
func (v *StringValue) Get() string { return v.val }

// IntValue implements Value for int configuration.
type IntValue struct {
	val int
	def int
}

// NewInt creates an IntValue with the given default.
func NewInt(def int) *IntValue {
	return &IntValue{val: def, def: def}
}

func (v *IntValue) String() string { return strconv.Itoa(v.val) }
func (v *IntValue) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("must be an integer, got: %q", s)
	}
	v.val = n
	return nil
}

// Get returns the current value.
func (v *IntValue) Get() int { return v.val }

// Int64Value implements Value for int64 configuration.
type Int64Value struct {
	val int64
	def int64
}

// NewInt64 creates an Int64Value with the given default.
func NewInt64(def int64) *Int64Value {
	return &Int64Value{val: def, def: def}
}

func (v *Int64Value) String() string { return strconv.FormatInt(v.val, 10) }
func (v *Int64Value) Set(s string) error {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("must be an integer, got: %q", s)
	}
	v.val = n
	return nil
}

// Get returns the current value.
func (v *Int64Value) Get() int64 { return v.val }
