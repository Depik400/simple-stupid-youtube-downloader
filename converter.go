package main

import (
	"bytes"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
)

// Converter интерфейс для конвертации файлов
type Converter interface {
	ConvertToMP4(inputFile string) (string, error)
}

type FFmpegConverter struct {
	logger *log.Logger
}

func NewFFmpegConverter(logger *log.Logger) *FFmpegConverter {
	return &FFmpegConverter{logger: logger}
}

func (c *FFmpegConverter) ConvertToMP4(inputFile string) (string, error) {
	// if strings.HasSuffix(inputFile, ".mp4") {
	// 	return inputFile, nil // Уже MP4, конвертация не нужна
	// }

	outputFile := strings.TrimSuffix(inputFile, filepath.Ext(inputFile)) + "_converted.mp4"
	cmd := exec.Command("ffmpeg", "-i", inputFile, "-c:v", "libx264", "-c:a", "aac", outputFile)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		c.logger.Printf("Ошибка конвертации в MP4: %v, output: %s", err, stderrBuf.String())
		return "", err
	}

	return outputFile, nil
}
