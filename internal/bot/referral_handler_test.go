package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

// TestReferralInviteLinkUsesRealBotUsername is a regression test for the bug
// where invite/share links carried the startup placeholder username
// (rs8kvn_bot_offline) instead of the real bot username.
//
// It mirrors main.go step 5 (handler built with an empty/placeholder
// BotConfig) followed by SetBotConfig(realBC) on step 7, and asserts the
// generated Telegram invite link uses the real username.
func TestReferralInviteLinkUsesRealBotUsername(t *testing.T) {
	cfg := &config.Config{}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()

	// Step 5: handler wired with an EMPTY (placeholder) bot config, exactly
	// like main.go before initBot replaces it.
	h := NewHandler(mockBot, cfg, mockDB, &BotConfig{}, nil, "")

	const realUsername = "rs8kvn_bot"
	h.SetBotConfig(&BotConfig{Username: realUsername})

	link, err := h.referral.generateInviteLink(context.Background(), 12345, linkTypeTelegram)
	if err != nil {
		t.Fatalf("generateInviteLink returned error: %v", err)
	}

	if !strings.Contains(link, "https://t.me/"+realUsername+"?start=share_") {
		t.Fatalf("invite link missing real username %q: got %q", realUsername, link)
	}
	if strings.Contains(link, "rs8kvn_bot_offline") {
		t.Fatalf("invite link leaked placeholder username: %q", link)
	}
	if strings.Contains(link, "https://t.me/?start=") {
		t.Fatalf("invite link has empty username: %q", link)
	}
}

// TestReferralInviteTextShowsRealBotUsername is a regression test for the bug
// where the "Share" page invite message text had an EMPTY @botUsername.
//
// It mirrors main.go step 5 (handler built with an empty/placeholder BotConfig)
// followed by SetBotConfig(realBC) on step 7, and asserts the text produced by
// ReferralHandler.keyboards.InviteLinkText contains the real @username.
func TestReferralInviteTextShowsRealBotUsername(t *testing.T) {
	cfg := &config.Config{}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()

	// Step 5: handler wired with an EMPTY (placeholder) bot config, exactly
	// like main.go before initBot replaces it.
	h := NewHandler(mockBot, cfg, mockDB, &BotConfig{}, nil, "")

	const realUsername = "rs8vpn_bot"
	h.SetBotConfig(&BotConfig{Username: realUsername})

	link, err := h.referral.generateInviteLink(context.Background(), 12345, linkTypeTelegram)
	if err != nil {
		t.Fatalf("generateInviteLink returned error: %v", err)
	}
	webLink := "https://example.com/i/x"
	text := h.referral.keyboards.InviteLinkText(link, webLink)

	if !strings.Contains(text, "@"+realUsername) {
		t.Fatalf("InviteLinkText missing @%s: got %q", realUsername, text)
	}
}
