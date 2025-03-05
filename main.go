package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

// DownloadResult содержит результат скачивания
type DownloadResult struct {
	FilePath string
	Error    error
}

// FileRecord хранит информацию о файле для очистки
type FileRecord struct {
	Path      string
	CreatedAt time.Time
}

func main() {
	// Инициализируем логгер
	logFile, err := os.OpenFile("server.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Ошибка открытия файла логов: %v", err)
	}
	defer logFile.Close()
	logger := log.New(os.Stdout, "INFO: ", log.LstdFlags|log.Lshortfile)

	// Получаем конфигурацию
	config := NewConfig(logger)

	// Инициализируем зависимости
	storage, err := NewSQLiteStorage("downloads.db", logger)
	if err != nil {
		logger.Fatalf("Ошибка инициализации хранилища: %v", err)
	}

	downloader := NewYTDLPDownloader(logger)
	converter := NewFFmpegConverter(logger)
	telegramBot, err := NewTelegramBot(config.TelegramToken, config.APIEndpoint, logger)
	if err != nil {
		logger.Fatalf("Ошибка инициализации Telegram-бота: %v", err)
	}

	// Создаём сервис
	service := NewDownloadService(downloader, converter, storage, telegramBot, logger)

	// Запускаем очистку
	go storage.CleanupWorker(24 * time.Hour)

	// Запускаем Telegram-бота
	go telegramBot.Start(service)

	// Настраиваем HTTP-сервер
	http.HandleFunc("/download", service.HTTPDownloadHandler)
	logger.Println("Сервер запущен на http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Config хранит конфигурацию приложения
type Config struct {
	TelegramToken string
	APIEndpoint   string
}

func NewConfig(logger *log.Logger) Config {
	token := "7507708709:AAHVOuavXE5gEg50V2K-A1A9W0nOIGFjt0s"
	if token == "" {
		logger.Fatalf("TELEGRAM_TOKEN не задан")
	}
	endpoint := "http://localhost:4040/bot%s/%s"
	if endpoint == "" {
		logger.Fatalf("TELEGRAM_API_ENDPOINT не задан")
	}
	logger.Printf("TELEGRAM_TOKEN: %s", token)
	logger.Printf("TELEGRAM_API_ENDPOINT: %s", endpoint)
	return Config{TelegramToken: token, APIEndpoint: endpoint}
}
