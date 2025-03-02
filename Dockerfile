# Используем официальный образ Go как базовый для сборки
FROM golang:1.21 AS builder

# Устанавливаем рабочую директорию внутри контейнера
WORKDIR /app

# Копируем go.mod и go.sum для установки зависимостей
COPY go.mod go.sum ./
RUN go mod download

# Копируем весь код в контейнер
COPY . .

# Собираем приложение
RUN go build -o youtube_downloader .

# Используем минимальный образ для запуска
FROM ubuntu:22.04

# Устанавливаем необходимые зависимости: yt-dlp, ffmpeg, pip
RUN apt-get update && \
    apt-get install -y python3-pip ffmpeg && \
    pip3 install yt-dlp && \
    rm -rf /var/lib/apt/lists/*

# Копируем скомпилированный бинарник из сборочного этапа
COPY --from=builder /app/youtube_downloader /usr/local/bin/youtube_downloader

# Устанавливаем рабочую директорию
WORKDIR /data

# Указываем порт, который будет использоваться
EXPOSE 8080

# Команда для запуска сервера
CMD ["youtube_downloader"]