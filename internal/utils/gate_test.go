package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIntervalGateBoundsFreshEntries(t *testing.T) {
	g := NewIntervalGate(time.Hour, 2)
	now := time.Now()
	assert.True(t, g.Allow("a", now))
	assert.False(t, g.Allow("a", now))
	assert.True(t, g.Allow("b", now))
	assert.True(t, g.Allow("c", now))
	assert.Len(t, g.seen, 1)
	assert.False(t, g.Allow("c", now))
}
