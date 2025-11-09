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
	spotifyNoPlaylist := flag.Bool("no-playlist", false, "Don't activate the default playlist defined in .env")
	flag.Parse()

	if *spotifyAutostart && *spotifyNoPlaylist {
		log.Fatal("Cannot have autostart without a default playlist")
	}

	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file", err)
	}

	slog.SetLogLoggerLevel(slog.LevelDebug)

	ApiHandlersSetup()
	go ApiHandlersRun()

	SpotifyClientSetup(*spotifyAutostart)
	go SpotifyPlaylistHandlerRun(*spotifyNoPlaylist)

	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	<-signalCh
	slog.Info("Exit signal received. Good bye.")
}
