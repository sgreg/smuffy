package main

import (
	"flag"
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
	spotifyAutostart := flag.Bool("autostart", false, "Start Spotify handling right away")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file", err)
	}

	slog.SetLogLoggerLevel(slog.LevelDebug)

	HandlersSetup()
	go HandlersRun()

	SpotifyClientSetup(*spotifyAutostart)
	go SpotifyPlaylistDump()

	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	<-signalCh
	slog.Info("Exit signal received. Good bye.")
}
