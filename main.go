package main

// Taken from https://github.com/zmb3/spotify/blob/master/examples/authenticate/authcode/authenticate.go
// Adjusted to own callback, and adding godotenv to load env vars directly from .env file

// This example demonstrates how to authenticate with Spotify using the authorization code flow.
// In order to run this example yourself, you'll need to:
//
//  1. Register an application at: https://developer.spotify.com/my-applications/
//       - Use "http://localhost:8080/callback" as the redirect URI
//  2. Set the SPOTIFY_ID environment variable to the client ID you got in step 1.
//  3. Set the SPOTIFY_SECRET environment variable to the client secret from step 1.

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/zmb3/spotify/v2/auth"

	"github.com/zmb3/spotify/v2"
)

// redirectURI is the OAuth redirect URI for the application.
// You must register an application at Spotify's developer portal
// and enter this value.
const redirectURI = "http://127.0.0.1:58071/callback"

var (
	auth  *spotifyauth.Authenticator
	ch    = make(chan *spotify.Client)
	state = "abc123"
)

func main() {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file", err)
	}

	// first start an HTTP server
	http.HandleFunc("/callback", completeAuth)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Got request for:", r.URL.String())
	})
	go func() {
		err := http.ListenAndServe(":58071", nil)
		if err != nil {
			log.Fatal(err)
		}
	}()

	auth = spotifyauth.New(
		spotifyauth.WithRedirectURL(redirectURI),
		spotifyauth.WithScopes(
			spotifyauth.ScopeUserReadPrivate,
			spotifyauth.ScopeUserReadCurrentlyPlaying,
			spotifyauth.ScopeUserReadRecentlyPlayed,
		))

	url := auth.AuthURL(state)
	fmt.Println("Please log in to Spotify by visiting the following page in your browser:", url)

	// wait for auth to complete
	client := <-ch

	// use the client to make calls that require authorization
	user, err := client.CurrentUser(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("You are logged in as:", user.ID)

	go SpotifyPlaylistDump(client)

	exitSignal := make(chan os.Signal)
	signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)
	<-exitSignal
	slog.Info("Exit signal received. Good bye.")
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}
	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		log.Fatalf("State mismatch: %s != %s\n", st, state)
	}

	// use the token to get an authenticated client
	client := spotify.New(auth.Client(r.Context(), tok))
	fmt.Fprintf(w, "Login Completed!")
	ch <- client
}

func getEnv(key string, fallback string) string {
	if value, found := os.LookupEnv(key); found {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, found := os.LookupEnv(key); found {
		if parseInt, err := strconv.Atoi(value); err == nil {
			return parseInt
		} else {
			slog.Warn("Failed to parse env var key '%s', using fallback %d", key, fallback)
		}
	}
	return fallback
}

func SpotifyPlaylistDump(client *spotify.Client) {
	playlistRequestIntervalMinutes := getEnvInt("PLAYLIST_REQUEST_INTERVAL_MINUTES", 10)
	slog.Debug("Setting up Spotify Playlist Dumper")
	waitDuration := time.Duration(playlistRequestIntervalMinutes) * time.Minute

	ctx := context.Background()

	for {
		playing, err := client.PlayerCurrentlyPlaying(ctx)
		if err != nil {
			slog.Error("Cannot get what's playing now", "err", err)
		} else {
			song := fmt.Sprintf("%s - %s", playing.Item.Artists[0].Name, playing.Item.Name)
			slog.Info("Playing: " + song)
		}

		songs, err := client.PlayerRecentlyPlayed(ctx)
		if err != nil {
			slog.Error("Cannot get recently played", "err", err)
		} else {
			slog.Info("Recently played:")
			for index, item := range songs {
				slog.Info(fmt.Sprintf("    %02d: [%s] %s - %s",
					index+1,
					item.PlayedAt.Format("2006-01-02 15:04:05 -0700 MST"),
					item.Track.Artists[0].Name,
					item.Track.Name))
			}
		}

		time.Sleep(waitDuration)
	}
}
