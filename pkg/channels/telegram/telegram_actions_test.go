package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newTestChannelWithBus(t *testing.T, caller *stubCaller, msgBus *bus.MessageBus) *TelegramChannel {
	t.Helper()

	bot, err := telego.NewBot(testToken,
		telego.WithAPICaller(caller),
		telego.WithRequestConstructor(&stubConstructor{}),
		telego.WithDiscardLogger(),
	)
	require.NoError(t, err)

	base := channels.NewBaseChannel("telegram", nil, msgBus, nil,
		channels.WithMaxMessageLength(4000),
	)
	base.SetRunning(true)

	ch := &TelegramChannel{
		BaseChannel:  base,
		bot:          bot,
		chatIDs:      make(map[string]int64),
		bc:           &config.Channel{Type: config.ChannelTelegram, Enabled: true},
		tgCfg:        &config.TelegramSettings{},
		progress:     channels.NewToolFeedbackAnimator(nil),
		actionTokens: make(map[string]telegramActionToken),
	}
	base.SetOwner(ch)
	return ch
}

func TestSupportsMessageActions(t *testing.T) {
	ch := newTestChannel(t, &stubCaller{})
	if !ch.SupportsMessageActions() {
		t.Fatal("expected Telegram channel to support message actions")
	}
}

func TestBuildActionKeyboard_RegistersSingleUseToken(t *testing.T) {
	ch := newTestChannel(t, &stubCaller{})
	msg := bus.OutboundMessage{
		SessionKey: "sess-1",
		Actions:    []bus.MessageAction{{Type: bus.ActionContinue, Label: "Continue"}},
	}

	kb := ch.buildActionKeyboard(msg, "chat-9")
	require.NotNil(t, kb, "expected a keyboard for a continue action")
	require.Len(t, kb.InlineKeyboard, 1)
	require.Len(t, kb.InlineKeyboard[0], 1)

	button := kb.InlineKeyboard[0][0]
	require.Equal(t, "Continue", button.Text)
	require.True(t, strings.HasPrefix(button.CallbackData, callbackTokenPrefix),
		"callback data should carry the opaque token prefix, got %q", button.CallbackData)
	require.LessOrEqual(t, len([]byte(button.CallbackData)), 64,
		"callback data must fit Telegram's 64-byte limit")

	// First resolve succeeds and consumes the token.
	rec, ok := ch.resolveActionToken(button.CallbackData)
	require.True(t, ok)
	require.Equal(t, bus.ActionContinue, rec.action)
	require.Equal(t, "chat-9", rec.chatID)
	require.Equal(t, "sess-1", rec.sessionID)

	// Second resolve fails (single-use).
	_, ok = ch.resolveActionToken(button.CallbackData)
	require.False(t, ok, "token should be single-use")
}

func TestBuildActionKeyboard_NoActions(t *testing.T) {
	ch := newTestChannel(t, &stubCaller{})
	if kb := ch.buildActionKeyboard(bus.OutboundMessage{}, "chat-1"); kb != nil {
		t.Fatal("expected nil keyboard when there are no actions")
	}
}

func TestResolveActionToken_Expired(t *testing.T) {
	ch := newTestChannel(t, &stubCaller{})
	data := ch.registerActionToken(bus.ActionContinue, "chat-1", "sess-1")

	// Force expiry.
	token := strings.TrimPrefix(data, callbackTokenPrefix)
	ch.actionMu.Lock()
	rec := ch.actionTokens[token]
	rec.expiresAt = time.Now().Add(-time.Second)
	ch.actionTokens[token] = rec
	ch.actionMu.Unlock()

	if _, ok := ch.resolveActionToken(data); ok {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestResolveActionToken_UnknownPrefix(t *testing.T) {
	ch := newTestChannel(t, &stubCaller{})
	if _, ok := ch.resolveActionToken("something-else"); ok {
		t.Fatal("expected non-action callback data to be ignored")
	}
}

func TestSend_AttachesContinueKeyboard(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	msg := bus.OutboundMessage{
		Context:    bus.NewOutboundContext("telegram", "12345", ""),
		SessionKey: "sess-1",
		Content:    "Reached the tool-iteration limit.",
		Actions:    []bus.MessageAction{{Type: bus.ActionContinue, Label: "Continue"}},
	}

	ids, err := ch.Send(context.Background(), msg)
	require.NoError(t, err)
	require.NotEmpty(t, ids)
	require.Len(t, caller.calls, 1)

	// Verify the request body carried a reply_markup with our button.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(caller.calls[0].Data.BodyRaw, &payload))
	markup, ok := payload["reply_markup"].(map[string]any)
	require.True(t, ok, "expected reply_markup in the sent payload: %v", payload)
	rows, ok := markup["inline_keyboard"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 1)
}

func TestHandleCallbackQuery_InjectsInboundAndDeletesMessage(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	var deleted bool
	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			switch {
			case strings.Contains(url, "answerCallbackQuery"):
				return &ta.Response{Ok: true, Result: json.RawMessage(`true`)}, nil
			case strings.Contains(url, "deleteMessage"):
				deleted = true
				return &ta.Response{Ok: true, Result: json.RawMessage(`true`)}, nil
			default:
				return successResponse(t), nil
			}
		},
	}
	ch := newTestChannelWithBus(t, caller, msgBus)

	data := ch.registerActionToken(bus.ActionContinue, "555", "sess-1")

	chatMsg := &telego.Message{
		MessageID: 42,
		Chat:      telego.Chat{ID: 555, Type: "private"},
		From:      &telego.User{ID: 777},
	}
	query := telego.CallbackQuery{
		ID:      "cb-1",
		From:    telego.User{ID: 777},
		Data:    data,
		Message: chatMsg,
	}

	require.NoError(t, ch.handleCallbackQuery(context.Background(), query))
	require.True(t, deleted, "expected the actionable message to be deleted")

	select {
	case in := <-msgBus.InboundChan():
		require.Equal(t, "telegram", in.Context.Channel)
		require.Equal(t, "555", in.Context.ChatID)
		require.Equal(t, continueMessageContent, in.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("expected an inbound continue message to be published")
	}
}

func TestHandleCallbackQuery_UnknownTokenNoInbound(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	caller := &stubCaller{
		callFn: func(ctx context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			return &ta.Response{Ok: true, Result: json.RawMessage(`true`)}, nil
		},
	}
	ch := newTestChannelWithBus(t, caller, msgBus)

	query := telego.CallbackQuery{ID: "cb-1", Data: "unknown"}
	require.NoError(t, ch.handleCallbackQuery(context.Background(), query))

	select {
	case in := <-msgBus.InboundChan():
		t.Fatalf("did not expect an inbound message, got %+v", in)
	case <-time.After(200 * time.Millisecond):
	}
}
