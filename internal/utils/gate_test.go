package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// base — фиксированная точка отсчёта: now инжектируется в Allow, поэтому
// все проверки детерминированы.
var gateBase = time.Unix(1700000000, 0)

func TestIntervalGate(t *testing.T) {
	t.Parallel()

	t.Run("allow then deny in window then allow after every", func(t *testing.T) {
		t.Parallel()

		g := NewIntervalGate(time.Minute, 0)

		steps := []struct {
			offset time.Duration
			want   bool
		}{
			{0, true},
			{time.Second, false},
			{59 * time.Second, false},
			{time.Minute, true},
			{time.Minute + time.Second, false},
		}

		for i, s := range steps {
			got := g.Allow("key", gateBase.Add(s.offset))
			assert.Equal(t, s.want, got, "step %d (+%s)", i, s.offset)
		}
	})

	t.Run("keys are independent", func(t *testing.T) {
		t.Parallel()

		g := NewIntervalGate(time.Minute, 0)

		assert.True(t, g.Allow("a", gateBase))
		assert.True(t, g.Allow("b", gateBase))
		assert.False(t, g.Allow("a", gateBase.Add(time.Second)))
		assert.False(t, g.Allow("b", gateBase.Add(time.Second)))
		assert.True(t, g.Allow("c", gateBase.Add(time.Second)))
	})

	t.Run("prune removes expired keys at threshold", func(t *testing.T) {
		t.Parallel()

		g := NewIntervalGate(time.Minute, 2)

		assert.True(t, g.Allow("a", gateBase))
		// len(seen)=1 < pruneAt: "a" ещё держится
		assert.True(t, g.Allow("b", gateBase.Add(2*time.Minute)))
		// len(seen)=2 >= pruneAt: просроченный "a" вытесняется,
		// освобождённого слота хватает — полная очистка не нужна
		assert.True(t, g.Allow("c", gateBase.Add(2*time.Minute)))

		g.mu.Lock()
		defer g.mu.Unlock()

		assert.Len(t, g.seen, 2, "ожидались только свежие ключи")
		_, hasA := g.seen["a"]
		_, hasB := g.seen["b"]
		_, hasC := g.seen["c"]
		assert.False(t, hasA, "просроченный ключ должен быть вытеснен")
		assert.True(t, hasB)
		assert.True(t, hasC)
	})

	t.Run("prune evicts fresh keys when map is full and zero disables pruning", func(t *testing.T) {
		t.Parallel()

		g := NewIntervalGate(time.Minute, 2)

		assert.True(t, g.Allow("a", gateBase))
		assert.True(t, g.Allow("b", gateBase))
		// "a" и "b" свежие, но len(seen) >= pruneAt: карта забита, вытеснить
		// нечего — fallback-очистка стирает всю карту, выживает новый ключ.
		assert.True(t, g.Allow("c", gateBase.Add(time.Second)))
		assert.Len(t, g.seen, 1, "забитая свежими ключами карта очищается целиком")
		_, hasC := g.seen["c"]
		assert.True(t, hasC)

		// pruneAt=0: очистка отключена, карта растёт неограниченно.
		g0 := NewIntervalGate(time.Minute, 0)
		for _, k := range []string{"k1", "k2", "k3", "k4", "k5"} {
			assert.True(t, g0.Allow(k, gateBase))
		}
		g0.mu.Lock()
		assert.Len(t, g0.seen, 5)
		g0.mu.Unlock()
	})
}
