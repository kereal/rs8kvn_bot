package bot_test

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
)

func TestCompileTimeInterfaceChecks(t *testing.T) {
	var _ interfaces.BotAPI = (*tgbotapi.BotAPI)(nil)
}
