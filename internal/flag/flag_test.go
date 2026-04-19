package flag

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- StringValue Tests ---

func TestStringValue_Default(t *testing.T) {
	t.Parallel()

	v := NewString("hello")
	assert.Equal(t, "hello", v.Get())
	assert.Equal(t, "hello", v.String())
}

func TestStringValue_Set(t *testing.T) {
	t.Parallel()

	v := NewString("default")
	err := v.Set("world")
	require.NoError(t, err)
	assert.Equal(t, "world", v.Get())
}

func TestStringValue_SetEmpty(t *testing.T) {
	t.Parallel()

	v := NewString("default")
	err := v.Set("")
	require.NoError(t, err)
	assert.Equal(t, "", v.Get())
}

// --- IntValue Tests ---

func TestIntValue_Default(t *testing.T) {
	t.Parallel()

	v := NewInt(42)
	assert.Equal(t, 42, v.Get())
	assert.Equal(t, "42", v.String())
}

func TestIntValue_Set(t *testing.T) {
	t.Parallel()

	v := NewInt(0)
	err := v.Set("100")
	require.NoError(t, err)
	assert.Equal(t, 100, v.Get())
}

func TestIntValue_SetNegative(t *testing.T) {
	t.Parallel()

	v := NewInt(0)
	err := v.Set("-5")
	require.NoError(t, err)
	assert.Equal(t, -5, v.Get())
}

func TestIntValue_SetInvalid(t *testing.T) {
	t.Parallel()

	v := NewInt(0)
	err := v.Set("notanumber")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must be an integer")
	assert.Equal(t, 0, v.Get())
}

func TestIntValue_SetFloat(t *testing.T) {
	t.Parallel()

	v := NewInt(0)
	err := v.Set("3.14")
	assert.Error(t, err)
}

// --- Int64Value Tests ---

func TestInt64Value_Default(t *testing.T) {
	t.Parallel()

	v := NewInt64(9999999999)
	assert.Equal(t, int64(9999999999), v.Get())
	assert.Equal(t, "9999999999", v.String())
}

func TestInt64Value_Set(t *testing.T) {
	t.Parallel()

	v := NewInt64(0)
	err := v.Set("1234567890123")
	require.NoError(t, err)
	assert.Equal(t, int64(1234567890123), v.Get())
}

func TestInt64Value_SetInvalid(t *testing.T) {
	t.Parallel()

	v := NewInt64(0)
	err := v.Set("abc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must be an integer")
}

// --- Registry Tests ---

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()

	r := New()
	s := NewString("default")
	r.Register("KEY", s)

	v := r.Value("KEY")
	require.NotNil(t, v)
	assert.Equal(t, "default", v.String())
}

func TestRegistry_ValueNotFound(t *testing.T) {
	t.Parallel()

	r := New()
	assert.Nil(t, r.Value("MISSING"))
}

func TestRegistry_Names(t *testing.T) {
	t.Parallel()

	r := New()
	r.Register("A", NewString("1"))
	r.Register("B", NewString("2"))
	r.Register("C", NewString("3"))

	names := r.Names()
	assert.Len(t, names, 3)
	assert.Contains(t, names, "A")
	assert.Contains(t, names, "B")
	assert.Contains(t, names, "C")
}

// --- LoadEnv Tests ---

func TestRegistry_LoadEnv_FromEnv(t *testing.T) {
	t.Parallel()

	require.NoError(t, os.Setenv("TEST_FLAG_STRING", "from-env"))
	require.NoError(t, os.Setenv("TEST_FLAG_INT", "42"))
	defer func() {
		if err := os.Unsetenv("TEST_FLAG_STRING"); err != nil {
			t.Logf("Warning: failed to unset TEST_FLAG_STRING: %v", err)
		}
		if err := os.Unsetenv("TEST_FLAG_INT"); err != nil {
			t.Logf("Warning: failed to unset TEST_FLAG_INT: %v", err)
		}
	}()

	r := New()
	s := NewString("default")
	i := NewInt(0)
	r.Register("TEST_FLAG_STRING", s)
	r.Register("TEST_FLAG_INT", i)

	err := r.LoadEnv()
	require.NoError(t, err)

	assert.Equal(t, "from-env", s.Get())
	assert.Equal(t, 42, i.Get())
}

func TestRegistry_LoadEnv_DefaultWhenUnset(t *testing.T) {
	t.Parallel()

	require.NoError(t, os.Unsetenv("TEST_UNSET_FLAG"))

	r := New()
	s := NewString("fallback")
	r.Register("TEST_UNSET_FLAG", s)

	err := r.LoadEnv()
	require.NoError(t, err)
	assert.Equal(t, "fallback", s.Get())
}

func TestRegistry_LoadEnv_WhitespaceTrimmed(t *testing.T) {
	t.Parallel()

	require.NoError(t, os.Setenv("TEST_TRIM_FLAG", "  trimmed  "))
	defer func() {
		if err := os.Unsetenv("TEST_TRIM_FLAG"); err != nil {
			t.Logf("Warning: failed to unset TEST_TRIM_FLAG: %v", err)
		}
	}()

	r := New()
	s := NewString("")
	r.Register("TEST_TRIM_FLAG", s)

	err := r.LoadEnv()
	require.NoError(t, err)
	assert.Equal(t, "trimmed", s.Get())
}

func TestRegistry_LoadEnv_InvalidInt(t *testing.T) {
	t.Parallel()

	os.Setenv("TEST_BAD_INT", "notanint")
	defer os.Unsetenv("TEST_BAD_INT")

	r := New()
	i := NewInt(0)
	r.Register("TEST_BAD_INT", i)

	err := r.LoadEnv()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TEST_BAD_INT")
}

func TestRegistry_LoadEnv_IgnoresUnregistered(t *testing.T) {
	t.Parallel()

	os.Setenv("UNREGISTERED_VAR", "should-be-ignored")
	defer os.Unsetenv("UNREGISTERED_VAR")

	r := New()
	s := NewString("default")
	r.Register("OTHER_KEY", s)

	err := r.LoadEnv()
	require.NoError(t, err)
	assert.Equal(t, "default", s.Get())
}
