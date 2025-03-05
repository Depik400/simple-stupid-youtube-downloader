package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Downloader интерфейс для загрузки файлов
type Downloader interface {
	Download(url, outputFile, quality string, audioOnly bool) (string, error)
	Progress(output *bytes.Buffer) chan string
}

// YTDLPDownloader реализует загрузку через yt-dlp
type YTDLPDownloader struct {
	logger *log.Logger
}

func NewYTDLPDownloader(logger *log.Logger) *YTDLPDownloader {
	return &YTDLPDownloader{logger: logger}
}

func (d *YTDLPDownloader) Download(url, outputFile, quality string, audioOnly bool) (string, error) {
	finalFile := outputFile + ".mp4.webm"
	if audioOnly {
		finalFile = outputFile + ".mp3"
	}

	var cmd *exec.Cmd
	if audioOnly {
		// Скачиваем только аудио в MP3
		cmd = exec.Command("yt-dlp", "-x", "--audio-format", "mp3", "-o", finalFile, url)
	} else {
		// Скачиваем видео с указанным качеством
		qualityValue := strings.TrimPrefix(quality, "hd")
		cmd = exec.Command("yt-dlp", "-f", fmt.Sprintf("bestvideo[height<=?%s]+bestaudio/best", qualityValue), "-o", finalFile, url)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		d.logger.Printf("Ошибка скачивания с yt-dlp: %v, output: %s", err, stderrBuf.String())
		return "", err
	}

	// Если это видео, проверяем наличие аудиодорожки
	if !audioOnly {
		hasAudio, err := d.checkAudio(finalFile)
		if err != nil {
			d.logger.Printf("Ошибка проверки аудиодорожки: %v", err)
			return finalFile, nil // Продолжаем с тем, что есть
		}
		if !hasAudio {
			d.logger.Printf("Видео %s не содержит аудио, скачиваем отдельно", finalFile)
			audioFile, err := d.downloadAudio(url, outputFile)
			if err != nil {
				d.logger.Printf("Ошибка скачивания аудио: %v", err)
				return finalFile, nil // Продолжаем с видео без аудио
			}
			finalFile, err = d.mergeVideoAndAudio(finalFile, audioFile)
			if err != nil {
				d.logger.Printf("Ошибка склейки видео и аудио: %v", err)
				return finalFile, nil // Возвращаем исходное видео
			}
		}
	}

	return finalFile, nil
}

// checkAudio проверяет наличие аудиодорожки в файле
func (d *YTDLPDownloader) checkAudio(filePath string) (bool, error) {
	cmd := exec.Command("ffmpeg", "-i", filePath)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		output := stderrBuf.String()
		if strings.Contains(output, "Audio:") {
			return true, nil
		}
		return false, nil
	}
	return true, nil
}

// downloadAudio скачивает только аудиодорожку
func (d *YTDLPDownloader) downloadAudio(url, outputFile string) (string, error) {
	audioFile := outputFile + "_audio.mp3"
	cmd := exec.Command("yt-dlp", "-x", "--audio-format", "mp3", "-o", audioFile, url)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	cmd.Stdout = &stderrBuf

	if err := cmd.Run(); err != nil {
		d.logger.Printf("Ошибка скачивания аудио с yt-dlp: %v, output: %s", err, stderrBuf.String())
		return "", err
	}
	return audioFile, nil
}

// mergeVideoAndAudio склеивает видео и аудио через ffmpeg
func (d *YTDLPDownloader) mergeVideoAndAudio(videoFile, audioFile string) (string, error) {
	outputFile := strings.TrimSuffix(videoFile, ".mp4") + "_merged.mp4"
	cmd := exec.Command("ffmpeg", "-i", videoFile, "-i", audioFile, "-c:v", "copy", "-c:a", "aac", outputFile)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	cmd.Stdout = &stderrBuf

	if err := cmd.Run(); err != nil {
		d.logger.Printf("Ошибка склейки видео и аудио: %v, output: %s", err, stderrBuf.String())
		return videoFile, err
	}

	// Удаляем временные файлы
	os.Remove(videoFile)
	os.Remove(audioFile)

	return outputFile, nil
}

func (d *YTDLPDownloader) Progress(output *bytes.Buffer) chan string {
	progressChan := make(chan string)
	go func() {
		defer close(progressChan)
		for {
			lines := strings.Split(output.String(), "\n")
			for _, line := range lines {
				if strings.Contains(line, "[download]") && strings.Contains(line, "%") {
					parts := strings.Fields(line)
					for _, part := range parts {
						if strings.HasSuffix(part, "%") {
							progressChan <- strings.TrimSuffix(part, "%")
							break
						}
					}
				}
			}
			time.Sleep(5 * time.Second)
		}
	}()
	return progressChan
}
