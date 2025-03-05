package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramBot интерфейс для работы с Telegram
type TelegramBot interface {
	Start(service *DownloadService)
	SendMessage(chatID int64, text string) (tgbotapi.Message, error)
	SendFile(chatID int64, filePath string, audioOnly bool) error
	EditMessage(chatID int64, messageID int, text string) error
}

type TelegramBotImpl struct {
	bot    *tgbotapi.BotAPI
	logger *log.Logger
}

func NewTelegramBot(token, apiEndpoint string, logger *log.Logger) (*TelegramBotImpl, error) {
	bot, err := tgbotapi.NewBotAPIWithAPIEndpoint(token, apiEndpoint)
	if err != nil {
		return nil, err
	}
	return &TelegramBotImpl{bot: bot, logger: logger}, nil
}

func (t *TelegramBotImpl) Start(service *DownloadService) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := t.bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil && update.CallbackQuery == nil {
			continue
		}

		if update.Message != nil && strings.Contains(update.Message.Text, "youtube.com") {
			chatID := update.Message.Chat.ID
			url := update.Message.Text

			msg := tgbotapi.NewMessage(chatID, "Выберите опцию:")
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("Скачать видео", fmt.Sprintf("video:%s", url)),
					tgbotapi.NewInlineKeyboardButtonData("Скачать только аудио", fmt.Sprintf("audio:%s", url)),
				),
			)
			t.bot.Send(msg)
		}

		if update.CallbackQuery != nil {
			chatID := update.CallbackQuery.Message.Chat.ID
			data := update.CallbackQuery.Data

			if strings.HasPrefix(data, "video:") {
				url := strings.TrimPrefix(data, "video:")
				msg := tgbotapi.NewMessage(chatID, "Выберите качество видео:")
				msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("720p", fmt.Sprintf("quality:%s:hd720", url)),
						tgbotapi.NewInlineKeyboardButtonData("1080p", fmt.Sprintf("quality:%s:hd1080", url)),
					),
				)
				t.bot.Send(msg)
			} else if strings.HasPrefix(data, "audio:") {
				url := strings.TrimPrefix(data, "audio:")
				go service.StartDownload(chatID, url, "audio", true)
			} else if strings.HasPrefix(data, "quality:") {
				trimmed := strings.TrimPrefix(data, "quality:")
				if strings.HasSuffix(trimmed, ":hd720") {
					quality := "hd720"
					url := strings.TrimSuffix(trimmed, ":hd720")
					go service.StartDownload(chatID, url, quality, false)
				} else if strings.HasSuffix(trimmed, ":hd1080") {
					quality := "hd1080"
					url := strings.TrimSuffix(trimmed, ":hd1080")
					go service.StartDownload(chatID, url, quality, false)
				} else {
					t.SendMessage(chatID, "Ошибка: неверный формат качества")
				}
			}

			t.bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, ""))
			t.bot.Request(tgbotapi.NewDeleteMessage(chatID, update.CallbackQuery.Message.MessageID))
		}
	}
}

func (t *TelegramBotImpl) SendMessage(chatID int64, text string) (tgbotapi.Message, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	return t.bot.Send(msg)
}

func (t *TelegramBotImpl) SendFile(chatID int64, filePath string, audioOnly bool) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	var msg tgbotapi.Chattable
	if audioOnly {
		audio := tgbotapi.NewAudio(chatID, tgbotapi.FileReader{
			Name:   filepath.Base(filePath),
			Reader: file,
		})
		msg = audio
	} else {
		video := tgbotapi.NewVideo(chatID, tgbotapi.FileReader{
			Name:   filepath.Base(filePath),
			Reader: file,
		})
		msg = video
	}

	_, err = t.bot.Send(msg)
	return err
}

func (t *TelegramBotImpl) EditMessage(chatID int64, messageID int, text string) error {
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	_, err := t.bot.Send(msg)
	return err
}
