package xui_test

import (
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/xui"
)

func TestCompileTimeInterfaceChecks(t *testing.T) {
	var _ interfaces.XUIClient = (*xui.Client)(nil)
}
