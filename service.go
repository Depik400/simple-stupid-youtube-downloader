package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DownloadService основной сервис для обработки загрузок
type DownloadService struct {
	downloader Downloader
	converter  Converter
	storage    Storage
	telegram   TelegramBot
	logger     *log.Logger
	workerPool chan struct{}
}

func NewDownloadService(downloader Downloader, converter Converter, storage Storage, telegram TelegramBot, logger *log.Logger) *DownloadService {
	return &DownloadService{
		downloader: downloader,
		converter:  converter,
		storage:    storage,
		telegram:   telegram,
		logger:     logger,
		workerPool: make(chan struct{}, 5), // Максимум 5 воркеров
	}
}

func (s *DownloadService) StartDownload(chatID int64, url, quality string, audioOnly bool) {
	taskID, err := s.storage.SaveTask(url, quality)
	if err != nil {
		s.logger.Printf("Ошибка сохранения задачи: %v", err)
		s.telegram.SendMessage(chatID, "Ошибка обработки запроса")
		return
	}

	outputFile := "downloaded_" + fmt.Sprintf("%d", time.Now().UnixNano())
	resultChan := make(chan DownloadResult)

	sentMsg, _ := s.telegram.SendMessage(chatID, "Скачивание 0%")

	s.workerPool <- struct{}{}
	go func() {
		defer func() { <-s.workerPool }()
		filePath, err := s.downloadAndProcess(url, quality, outputFile, audioOnly, chatID, sentMsg.MessageID)
		result := DownloadResult{FilePath: filePath, Error: err}

		if err == nil {
			s.storage.StoreFileRecord(filePath)
			s.storage.UpdateTaskStatus(taskID, "completed", filePath)
			err = s.telegram.SendFile(chatID, filePath, audioOnly)
			if err != nil {
				s.logger.Fatal(err)
			}
			s.telegram.SendMessage(chatID, "Файл успешно загружен!")
		} else {
			s.storage.UpdateTaskStatus(taskID, "failed", "")
			s.telegram.SendMessage(chatID, fmt.Sprintf("Ошибка: %v", err))
		}

		resultChan <- result
	}()

	<-resultChan
}

func (s *DownloadService) downloadAndProcess(url, quality, outputFile string, audioOnly bool, chatID int64, messageID int) (string, error) {
	s.logger.Printf("Начинаем обработку: url=%s, quality=%s, output=%s, audioOnly=%v", url, quality, outputFile, audioOnly)

	downloadedFile, err := s.downloader.Download(url, outputFile, quality, audioOnly)
	if err != nil {
		return "", err
	}

	var finalFile string
	if !audioOnly {
		finalFile, err = s.converter.ConvertToMP4(downloadedFile)
		if err != nil {
			return "", err
		}
	} else {
		finalFile = downloadedFile
	}

	go func() {
		if chatID != 0 && messageID != 0 {
			for progress := range s.downloader.Progress(&bytes.Buffer{}) {
				s.telegram.EditMessage(chatID, messageID, fmt.Sprintf("Скачивание: %s%%", progress))
			}
		}
	}()

	s.logger.Printf("Обработка завершена: %s", finalFile)
	return finalFile, nil
}

func (s *DownloadService) HTTPDownloadHandler(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	quality := r.URL.Query().Get("quality")

	if url == "" {
		http.Error(w, "Параметр 'url' обязателен", http.StatusBadRequest)
		return
	}
	if quality == "" {
		quality = "hd720"
	}

	filePath, err := s.storage.GetCompletedFile(url, quality)
	if err == nil && filePath != "" {
		s.logger.Printf("Используем уже скачанный файл: %s", filePath)
		serveFile(w, filePath)
		return
	}

	s.StartDownload(0, url, quality, false) // chatID=0 для HTTP-запросов
}

func serveFile(w http.ResponseWriter, filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("Ошибка открытия файла: %v", err)
		http.Error(w, fmt.Sprintf("Ошибка открытия файла: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(filePath)))
	w.Header().Set("Content-Type", "video/mp4")
	_, err = io.Copy(w, file)
	if err != nil {
		log.Printf("Ошибка при отправке файла клиенту: %v", err)
	}
}
