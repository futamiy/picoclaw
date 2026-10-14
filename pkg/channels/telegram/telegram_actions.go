// PicoClaw - Ultra-lightweight personal AI agent

package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	// callbackTokenPrefix identifies our opaque action tokens in callback_data.
	callbackTokenPrefix = "pc:act:"
	// actionTokenTTL bounds how long a rendered action button stays valid.
	actionTokenTTL = 24 * time.Hour
	// continueMessageContent is the synthetic user instruction injected when a
	// user presses a "Continue" action button. It travels through the normal
	// agent path so the resumed turn gets full session history.
	continueMessageContent = "Continue"
)

// SupportsMessageActions implements channels.ContinueCapable. Telegram can
// render inline-keyboard action buttons.
func (c *TelegramChannel) SupportsMessageActions() bool {
	return true
}

// registerActionToken stores an action token server-side and returns the opaque
// callback_data string to embed in the inline keyboard button.
func (c *TelegramChannel) registerActionToken(action, chatID, sessionID string) string {
	token := newActionToken() // short, unique token
	c.actionMu.Lock()
	defer c.actionMu.Unlock()
	if c.actionTokens == nil {
		c.actionTokens = make(map[string]telegramActionToken)
	}
	c.pruneExpiredActionTokensLocked()
	c.actionTokens[token] = telegramActionToken{
		action:    action,
		chatID:    chatID,
		sessionID: sessionID,
		expiresAt: time.Now().Add(actionTokenTTL),
	}
	return callbackTokenPrefix + token
}

// resolveActionToken looks up and consumes (single-use) an action token.
func (c *TelegramChannel) resolveActionToken(callbackData string) (telegramActionToken, bool) {
	if !strings.HasPrefix(callbackData, callbackTokenPrefix) {
		return telegramActionToken{}, false
	}
	token := strings.TrimPrefix(callbackData, callbackTokenPrefix)
	c.actionMu.Lock()
	defer c.actionMu.Unlock()
	c.pruneExpiredActionTokensLocked()
	rec, ok := c.actionTokens[token]
	if !ok {
		return telegramActionToken{}, false
	}
	delete(c.actionTokens, token)
	if time.Now().After(rec.expiresAt) {
		return telegramActionToken{}, false
	}
	return rec, true
}

func (c *TelegramChannel) pruneExpiredActionTokensLocked() {
	now := time.Now()
	for k, v := range c.actionTokens {
		if now.After(v.expiresAt) {
			delete(c.actionTokens, k)
		}
	}
}

// buildActionKeyboard renders the inline keyboard for the actions attached to an
// outbound message, registering a fresh callback token per button. It returns
// nil when there are no renderable actions.
func (c *TelegramChannel) buildActionKeyboard(msg bus.OutboundMessage, chatID string) *telego.InlineKeyboardMarkup {
	if len(msg.Actions) == 0 {
		return nil
	}
	var buttons []telego.InlineKeyboardButton
	for _, action := range msg.Actions {
		if action.Type != bus.ActionContinue {
			continue
		}
		data := c.registerActionToken(action.Type, chatID, msg.SessionKey)
		button := tu.InlineKeyboardButton(action.Label)
		button.CallbackData = data
		buttons = append(buttons, button)
	}
	if len(buttons) == 0 {
		return nil
	}
	return tu.InlineKeyboard(tu.InlineKeyboardRow(buttons...))
}

// newActionToken returns a short random hex token (well within Telegram's
// 64-byte callback_data limit once prefixed).
func newActionToken() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to a time-based value; collisions are handled by map overwrite
		// and the token is only a lookup handle, not a secret.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// handleCallbackQuery processes inline-keyboard button presses. It acknowledges
// the callback, removes the now-stale actionable message, and injects a
// synthetic inbound user message that resumes the turn through the normal agent
// path (so notifications ring and full history is available).
func (c *TelegramChannel) handleCallbackQuery(ctx context.Context, query telego.CallbackQuery) error {
	// Always acknowledge promptly so the client spinner stops.
	_ = c.bot.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})

	rec, ok := c.resolveActionToken(query.Data)
	if !ok {
		logger.DebugCF("telegram", "Ignoring unknown/expired action callback", map[string]any{
			"data": query.Data,
		})
		return nil
	}
	if rec.action != bus.ActionContinue {
		return nil
	}

	chatIDInt, chatID, ok := telegramCallbackChat(query)
	if !ok {
		logger.WarnC("telegram", "Action callback without resolvable chat")
		return nil
	}

	// Remove the limit/error message so the continued answer arrives as a fresh,
	// notification-worthy message instead of an in-place edit.
	if query.Message != nil {
		if msg := query.Message.Message(); msg != nil {
			_ = c.bot.DeleteMessage(ctx, &telego.DeleteMessageParams{
				ChatID:    tu.ID(chatIDInt),
				MessageID: msg.GetMessageID(),
			})
		}
	}

	c.publishContinueInbound(ctx, chatID, query)
	return nil
}

func telegramCallbackChat(query telego.CallbackQuery) (int64, string, bool) {
	if query.Message == nil {
		return 0, "", false
	}
	msg := query.Message.Message()
	if msg == nil || msg.Chat.ID == 0 {
		return 0, "", false
	}
	return msg.Chat.ID, fmt.Sprintf("%d", msg.Chat.ID), true
}

// publishContinueInbound injects a synthetic "Continue" user message through the
// standard channel inbound path (so session routing, typing/placeholder
// indicators and the agent turn all behave as if the user typed it). The inbound
// context is reconstructed from the callback message's chat/thread so it targets
// the same session. Best-effort: failures are logged, not surfaced.
func (c *TelegramChannel) publishContinueInbound(ctx context.Context, chatID string, query telego.CallbackQuery) {
	inboundCtx := bus.InboundContext{
		Channel:  c.Name(),
		ChatID:   chatID,
		ChatType: telegramChatTypeFromCallback(query),
		SenderID: telegramSenderIDFromCallback(query),
	}
	if query.Message != nil {
		if msg := query.Message.Message(); msg != nil && msg.MessageThreadID != 0 {
			inboundCtx.TopicID = fmt.Sprintf("%d", msg.MessageThreadID)
		}
	}
	sender := bus.SenderInfo{
		Platform:    "telegram",
		PlatformID:  inboundCtx.SenderID,
		CanonicalID: "telegram:" + inboundCtx.SenderID,
	}
	if err := c.HandleInboundContext(ctx, chatID, continueMessageContent, nil, inboundCtx, sender); err != nil {
		logger.WarnCF("telegram", "Failed to publish continue inbound", map[string]any{
			"chat_id": chatID,
			"error":   err.Error(),
		})
	}
}

func telegramSenderIDFromCallback(query telego.CallbackQuery) string {
	if query.From.ID == 0 {
		return ""
	}
	return fmt.Sprintf("%d", query.From.ID)
}

func telegramChatTypeFromCallback(query telego.CallbackQuery) string {
	if query.Message == nil {
		return ""
	}
	msg := query.Message.Message()
	if msg == nil {
		return ""
	}
	switch msg.Chat.Type {
	case "private":
		return "direct"
	case "group", "supergroup":
		return "group"
	case "channel":
		return "channel"
	default:
		return msg.Chat.Type
	}
}

// startActionCallbacks registers the callback-query handler. Safe to call once
// during Start; a nil bot handler is a no-op.
func (c *TelegramChannel) startActionCallbacks(bh *th.BotHandler) {
	if bh == nil {
		return
	}
	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		return c.handleCallbackQuery(ctx, query)
	}, th.AnyCallbackQuery())
}
