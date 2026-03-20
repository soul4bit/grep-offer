package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type TelegramBot struct {
	token       string
	adminChatID int64
	httpClient  *http.Client
}

func NewTelegramBot(token string, adminChatID int64) *TelegramBot {
	return &TelegramBot{
		token:       strings.TrimSpace(token),
		adminChatID: adminChatID,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (b *TelegramBot) SendRegistrationRequest(ctx context.Context, requestID int64, username, email string, createdAt time.Time) error {
	return b.call(ctx, "sendMessage", map[string]any{
		"chat_id": b.adminChatID,
		"text": strings.Join([]string{
			"Новая заявка на grep-offer.ru",
			"",
			fmt.Sprintf("Ник: %s", username),
			fmt.Sprintf("Email: %s", email),
			fmt.Sprintf("Время: %s UTC", createdAt.UTC().Format("2006-01-02 15:04:05")),
			"",
			"Жми approve, если пускаем дальше к письму.",
		}, "\n"),
		"reply_markup": map[string]any{
			"inline_keyboard": [][]map[string]string{
				{
					{
						"text":          "Approve",
						"callback_data": fmt.Sprintf("approve:%d", requestID),
					},
					{
						"text":          "Reject",
						"callback_data": fmt.Sprintf("reject:%d", requestID),
					},
				},
			},
		},
	})
}

func (b *TelegramBot) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	return b.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
		"show_alert":        false,
	})
}

func (b *TelegramBot) MarkRegistrationApproved(ctx context.Context, chatID int64, messageID int, username, email string) error {
	return b.call(ctx, "editMessageText", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text": strings.Join([]string{
			"Заявка апрувнута",
			"",
			fmt.Sprintf("Ник: %s", username),
			fmt.Sprintf("Email: %s", email),
			"",
			"Письмо с подтверждением уже отправлено.",
		}, "\n"),
	})
}

func (b *TelegramBot) MarkRegistrationRejected(ctx context.Context, chatID int64, messageID int, username, email string) error {
	return b.call(ctx, "editMessageText", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text": strings.Join([]string{
			"Заявка отклонена",
			"",
			fmt.Sprintf("Ник: %s", username),
			fmt.Sprintf("Email: %s", email),
		}, "\n"),
	})
}

func (b *TelegramBot) call(ctx context.Context, method string, payload map[string]any) error {
	if b == nil {
		return fmt.Errorf("telegram bot is nil")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.token, method),
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := b.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("telegram %s returned status %d", method, response.StatusCode)
	}

	return nil
}
