package database_test

import (
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
)

func TestCompileTimeInterfaceChecks(t *testing.T) {
	var _ interfaces.DatabaseService = (*database.Service)(nil)
}
