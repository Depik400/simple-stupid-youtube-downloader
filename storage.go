package main

import (
	"database/sql"
	"log"
	"os"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Storage интерфейс для работы с хранилищем
type Storage interface {
	SaveTask(url, quality string) (int64, error)
	UpdateTaskStatus(id int64, status, filePath string) error
	GetCompletedFile(url, quality string) (string, error)
	StoreFileRecord(filePath string)
	CleanupWorker(interval time.Duration)
}

type SQLiteStorage struct {
	db     *sql.DB
	files  sync.Map
	logger *log.Logger
}

func NewSQLiteStorage(dbPath string, logger *log.Logger) (*SQLiteStorage, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

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
		return nil, err
	}

	return &SQLiteStorage{db: db, logger: logger}, nil
}

func (s *SQLiteStorage) SaveTask(url, quality string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO downloads (url, quality, status) VALUES (?, ?, 'pending')", url, quality)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLiteStorage) UpdateTaskStatus(id int64, status, filePath string) error {
	_, err := s.db.Exec("UPDATE downloads SET status = ?, file_path = ? WHERE id = ?", status, filePath, id)
	return err
}

func (s *SQLiteStorage) GetCompletedFile(url, quality string) (string, error) {
	var filePath string
	err := s.db.QueryRow("SELECT file_path FROM downloads WHERE url = ? AND quality = ? AND status = 'completed'", url, quality).Scan(&filePath)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		s.db.Exec("DELETE FROM downloads WHERE file_path = ?", filePath)
		return "", nil
	}
	return filePath, nil
}

func (s *SQLiteStorage) StoreFileRecord(filePath string) {
	s.files.Store(filePath, FileRecord{
		Path:      filePath,
		CreatedAt: time.Now(),
	})
}

func (s *SQLiteStorage) CleanupWorker(interval time.Duration) {
	for {
		time.Sleep(interval)
		now := time.Now()
		s.files.Range(func(key, value interface{}) bool {
			record := value.(FileRecord)
			if now.Sub(record.CreatedAt) > interval {
				s.logger.Printf("Удаляем старый файл: %s", record.Path)
				os.Remove(record.Path)
				s.files.Delete(key)
				_, err := s.db.Exec("DELETE FROM downloads WHERE file_path = ?", record.Path)
				if err != nil {
					s.logger.Printf("Ошибка удаления записи из базы: %v", err)
				}
			}
			return true
		})
	}
}
