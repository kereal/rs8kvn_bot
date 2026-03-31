package bot

import (
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type BotConfig struct {
	Username                string
	ID                      int64
	FirstName               string
	IsBot                   bool
	CanJoinGroups           bool
	CanReadAllGroupMessages bool
	SupportsInlineQueries   bool
	loadedAt                time.Time
}

func NewBotConfig(botAPI *tgbotapi.BotAPI) (*BotConfig, error) {
	self := botAPI.Self
	return &BotConfig{
		Username:                self.UserName,
		ID:                      self.ID,
		FirstName:               self.FirstName,
		IsBot:                   self.IsBot,
		CanJoinGroups:           self.CanJoinGroups,
		CanReadAllGroupMessages: self.CanReadAllGroupMessages,
		SupportsInlineQueries:   self.SupportsInlineQueries,
		loadedAt:                time.Now(),
	}, nil
}

func (bc *BotConfig) Self() *tgbotapi.User {
	return &tgbotapi.User{
		ID:                      bc.ID,
		FirstName:               bc.FirstName,
		UserName:                bc.Username,
		IsBot:                   bc.IsBot,
		CanJoinGroups:           bc.CanJoinGroups,
		CanReadAllGroupMessages: bc.CanReadAllGroupMessages,
		SupportsInlineQueries:   bc.SupportsInlineQueries,
	}
}
