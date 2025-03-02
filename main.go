package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DownloadTask описывает задачу скачивания
type DownloadTask struct {
	ID         int
	URL        string
	Quality    string
	OutputFile string
	ResultChan chan DownloadResult
}

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

var (
	// Глобальный воркер пул
	workerPool chan struct{}
	// Хранилище для файлов (путь -> время создания)
	fileRecords sync.Map
	// Количество воркеров в пуле
	maxWorkers = 5
	// Логгер
	logger *log.Logger
	// База данных
	db *sql.DB
)

func main() {
	// Инициализируем логгер
	logFile, err := os.OpenFile("server.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Ошибка открытия файла логов: %v", err)
	}
	defer logFile.Close()
	logger = log.New(logFile, "INFO: ", log.LstdFlags|log.Lshortfile)

	// Проверяем наличие yt-dlp
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		logger.Fatalf("yt-dlp не установлен. Установите его: pip install yt-dlp")
	}

	// Проверяем наличие ffmpeg
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		logger.Fatalf("ffmpeg не установлен. Установите его: sudo apt-get install ffmpeg (Linux) или brew install ffmpeg (Mac)")
	}

	// Инициализируем базу данных
	var dbErr error
	db, dbErr = sql.Open("sqlite3", "downloads.db")
	if dbErr != nil {
		logger.Fatalf("Ошибка открытия базы данных: %v", dbErr)
	}
	defer db.Close()

	// Создаем таблицу для хранения запросов
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS downloads (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			quality TEXT NOT NULL,
			file_path TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		logger.Fatalf("Ошибка создания таблицы: %v", err)
	}

	// Инициализируем воркер пул
	workerPool = make(chan struct{}, maxWorkers)

	// Запускаем горутину для очистки старых файлов и записей
	go cleanupWorker()

	// Восстанавливаем незавершённые задачи
	go recoverPendingTasks()

	// Создаем HTTP-сервер
	http.HandleFunc("/download", downloadHandler)

	// Запускаем сервер на порту 8080
	logger.Println("Сервер запущен на http://localhost:8080")
	fmt.Println("Сервер запущен на http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// recoverPendingTasks восстанавливает незавершённые задачи
func recoverPendingTasks() {
	rows, err := db.Query("SELECT id, url, quality FROM downloads WHERE status = 'pending'")
	if err != nil {
		logger.Printf("Ошибка получения незавершённых задач: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var url, quality string
		if err := rows.Scan(&id, &url, &quality); err != nil {
			logger.Printf("Ошибка чтения незавершённой задачи: %v", err)
			continue
		}
		logger.Printf("Восстанавливаем задачу: id=%d, url=%s, quality=%s", id, url, quality)
		go processTask(DownloadTask{
			ID:         id,
			URL:        url,
			Quality:    quality,
			OutputFile: fmt.Sprintf("downloaded_%d", time.Now().UnixNano()),
			ResultChan: nil, // Не используем канал, т.к. это восстановление
		})
	}
}

// cleanupWorker периодически чистит старые файлы и записи в базе
func cleanupWorker() {
	for {
		time.Sleep(time.Hour) // Проверяем раз в час
		now := time.Now()
		fileRecords.Range(func(key, value interface{}) bool {
			record := value.(FileRecord)
			if now.Sub(record.CreatedAt) > 24*time.Hour {
				logger.Printf("Удаляем старый файл: %s", record.Path)
				os.Remove(record.Path)
				fileRecords.Delete(key)
				// Удаляем запись из базы данных
				_, err := db.Exec("DELETE FROM downloads WHERE file_path = ?", record.Path)
				if err != nil {
					logger.Printf("Ошибка удаления записи из базы: %v", err)
				}
			}
			return true
		})
	}
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	// Парсим параметры из GET-запроса
	url := r.URL.Query().Get("url")
	quality := r.URL.Query().Get("quality")

	// Если ссылка не указана, возвращаем ошибку
	if url == "" {
		http.Error(w, "Параметр 'url' обязателен", http.StatusBadRequest)
		return
	}

	// Если качество не указано, ставим по умолчанию 720p
	if quality == "" {
		quality = "hd720"
	}

	// Проверяем, есть ли уже обработанное видео
	var filePath string
	err := db.QueryRow("SELECT file_path FROM downloads WHERE url = ? AND quality = ? AND status = 'completed'", url, quality).Scan(&filePath)
	if err == nil && filePath != "" {
		if _, err := os.Stat(filePath); err == nil {
			logger.Printf("Используем уже скачанный файл: %s", filePath)
			serveFile(w, filePath)
			return
		}
		// Если файл не существует, удаляем запись
		_, err = db.Exec("DELETE FROM downloads WHERE file_path = ?", filePath)
		if err != nil {
			logger.Printf("Ошибка удаления записи из базы: %v", err)
		}
	}

	// Сохраняем запрос в базу со статусом pending
	res, err := db.Exec("INSERT INTO downloads (url, quality, status) VALUES (?, ?, 'pending')", url, quality)
	if err != nil {
		logger.Printf("Ошибка сохранения запроса в базу: %v", err)
		http.Error(w, "Ошибка обработки запроса", http.StatusInternalServerError)
		return
	}
	taskID, _ := res.LastInsertId()

	// Генерируем уникальное имя файла
	outputFile := fmt.Sprintf("downloaded_%d", time.Now().UnixNano())

	// Создаем канал для получения результата
	resultChan := make(chan DownloadResult)

	// Запускаем задачу в воркер пуле
	workerPool <- struct{}{}
	go func() {
		defer func() { <-workerPool }() // Освобождаем слот воркера
		processTask(DownloadTask{
			ID:         int(taskID),
			URL:        url,
			Quality:    quality,
			OutputFile: outputFile,
			ResultChan: resultChan,
		})
	}()

	// Ожидаем результат
	result := <-resultChan
	if result.Error != nil {
		// Обновляем статус в базе на failed
		_, err = db.Exec("UPDATE downloads SET status = 'failed' WHERE id = ?", taskID)
		if err != nil {
			logger.Printf("Ошибка обновления статуса в базе: %v", err)
		}
		http.Error(w, fmt.Sprintf("Ошибка обработки видео: %v", result.Error), http.StatusInternalServerError)
		return
	}

	// Обновляем статус в базе на completed и сохраняем путь к файлу
	_, err = db.Exec("UPDATE downloads SET status = 'completed', file_path = ? WHERE id = ?", result.FilePath, taskID)
	if err != nil {
		logger.Printf("Ошибка обновления статуса в базе: %v", err)
	}

	// Отдаём файл клиенту
	serveFile(w, result.FilePath)
}

func processTask(task DownloadTask) {
	filePath, err := downloadAndProcessVideo(task.URL, task.Quality, task.OutputFile)
	result := DownloadResult{FilePath: filePath, Error: err}

	// Сохраняем информацию о файле для очистки
	if err == nil {
		fileRecords.Store(filePath, FileRecord{
			Path:      filePath,
			CreatedAt: time.Now(),
		})
	}

	// Если это не восстановление, отправляем результат в канал
	if task.ResultChan != nil {
		task.ResultChan <- result
	} else if err != nil {
		// Если это восстановление и произошла ошибка, обновляем статус
		_, err := db.Exec("UPDATE downloads SET status = 'failed' WHERE id = ?", task.ID)
		if err != nil {
			logger.Printf("Ошибка обновления статуса при восстановлении: %v", err)
		}
	} else {
		// Если восстановление прошло успешно
		_, err := db.Exec("UPDATE downloads SET status = 'completed', file_path = ? WHERE id = ?", filePath, task.ID)
		if err != nil {
			logger.Printf("Ошибка обновления статуса при восстановлении: %v", err)
		}
	}
}

func downloadAndProcessVideo(url, quality, outputFile string) (string, error) {
	logger.Printf("Начинаем обработку: url=%s, quality=%s, output=%s", url, quality, outputFile)

	// Скачиваем через yt-dlp
	finalFile := outputFile + ".mp4.webm"
	qualityValue := strings.TrimPrefix(quality, "hd") // yt-dlp ожидает просто число, например "720"
	cmd := exec.Command("yt-dlp", "-f", fmt.Sprintf("bestvideo[height<=?%s]+bestaudio/best", qualityValue), "-o", finalFile, url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		logger.Printf("Ошибка скачивания с yt-dlp: %v", err)
		return "", fmt.Errorf("ошибка скачивания с yt-dlp: %v", err)
	}

	logger.Printf("Обработка завершена: %s", finalFile)
	return finalFile, nil
}

// Функция для отдачи файла клиенту
func serveFile(w http.ResponseWriter, filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		logger.Printf("Ошибка открытия файла: %v", err)
		http.Error(w, fmt.Sprintf("Ошибка открытия файла: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(filePath)))
	w.Header().Set("Content-Type", "video/mp4")

	_, err = io.Copy(w, file)
	if err != nil {
		logger.Printf("Ошибка при отправке файла клиенту: %v", err)
	}
}
