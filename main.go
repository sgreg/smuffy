package main

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
)

var (
	signalCh = make(chan os.Signal)
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file", err)
	}

	slog.SetLogLoggerLevel(slog.LevelDebug)

	ApiHandlersSetup()
	go ApiHandlersRun()

	SpotifyClientSetup()
	go SpotifyPlaylistHandlerRun()

	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	<-signalCh
	slog.Info("Exit signal received. Good bye.")
}
