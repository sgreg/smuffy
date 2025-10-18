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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"

	"github.com/zmb3/spotify/v2"
)

// redirectURI is the OAuth redirect URI for the application.
// You must register an application at Spotify's developer portal
// and enter this value.
const redirectURI = "http://127.0.0.1:58071/callback"

const SpotifyTokenCacheFile = ".cache"

var (
	auth     *spotifyauth.Authenticator
	ch       = make(chan *spotify.Client)
	state    = "abc123"
	signalCh = make(chan os.Signal)
	songsMap = make(map[spotify.URI]string)
)

func main() {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file", err)
	}

	// first start an HTTP server
	http.HandleFunc("/callback", completeAuth)
	http.HandleFunc("/songmap", getSongMap)
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

	var client *spotify.Client

	slog.Debug("Trying to load cached token")
	token, err := loadToken()
	if err != nil {
		slog.Info("Token not found, or unable to parse, auth required")
		url := auth.AuthURL(state)
		fmt.Println("Please log in to Spotify by visiting the following page in your browser:", url)
		// wait for auth to complete
		client = <-ch
	} else {
		slog.Debug("Cached token found")
		client = spotify.New(auth.Client(context.Background(), token))
	}

	// use the client to make calls that require authorization
	user, err := client.CurrentUser(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	slog.Info(fmt.Sprintf("You are logged in as: %s", user.ID))

	go SpotifyPlaylistDump(client)

	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	<-signalCh
	slog.Info("Exit signal received. Good bye.")
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	token, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}
	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		log.Fatalf("State mismatch: %s != %s\n", st, state)
	}

	slog.Debug("Saving token", "file", SpotifyTokenCacheFile)
	if err := saveToken(token); err != nil {
		slog.Warn("Failed to save token", "err", err)
	}

	client := spotify.New(auth.Client(r.Context(), token))
	fmt.Fprintf(w, "Login Completed!")
	ch <- client
}

func saveToken(token *oauth2.Token) error {
	file, err := os.Create(SpotifyTokenCacheFile)
	if err != nil {
		return err
	}
	// 'defer file.Close()' needs error handling, but not a lot of handling we can (or care to) do here
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	encoder := json.NewEncoder(file)
	if err = encoder.Encode(token); err != nil {
		return err
	}

	return nil
}

func loadToken() (*oauth2.Token, error) {
	file, err := os.Open(SpotifyTokenCacheFile)
	if err != nil {
		return nil, err
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	var token oauth2.Token

	decoder := json.NewDecoder(file)
	if err = decoder.Decode(&token); err != nil {
		return nil, err
	}

	return &token, nil
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
	playlistId := spotify.ID(getEnv("SPOTIFY_PLAYLIST_ID", "invalidListId"))
	playlistRequestIntervalMinutes := getEnvInt("PLAYLIST_REQUEST_INTERVAL_MINUTES", 10)
	slog.Debug("Setting up Spotify Playlist Dumper")
	waitDuration := time.Duration(playlistRequestIntervalMinutes) * time.Minute

	ctx := context.Background()

	loadPlaylistToMap(ctx, client, playlistId)

	for {
		playing, err := client.PlayerCurrentlyPlaying(ctx)
		if err != nil {
			slog.Error("Cannot get what's playing now", "err", err)
		} else {
			var currentlyPlayed string
			if playing.Item == nil {
				currentlyPlayed = "nothing"
			} else {
				currentlyPlayed = formatTrack(playing.Item.SimpleTrack)
			}
			slog.Info("Playing: " + currentlyPlayed)
		}

		songs, err := client.PlayerRecentlyPlayed(ctx)
		if err != nil {
			slog.Error("Cannot get recently played", "err", err)
		} else {
			slog.Info("Recently played:")
			var newSongs []spotify.ID
			for index, item := range songs {
				slog.Info(fmt.Sprintf("    %02d: [%s] %s",
					index+1,
					item.PlayedAt.Format("2006-01-02 15:04:05 -0700 MST"),
					formatTrack(item.Track),
				))
				if addToMap(item.Track) {
					newSongs = append(newSongs, item.Track.ID)
				}
			}
			updatePlaylist(ctx, client, playlistId, newSongs)
		}

		time.Sleep(waitDuration)
	}
}

func formatTrack(track spotify.SimpleTrack) string {
	return fmt.Sprintf("%s - %s", track.Artists[0].Name, track.Name)
}

func loadPlaylistToMap(ctx context.Context, client *spotify.Client, playlistId spotify.ID) {
	slog.Debug("Getting predefined playlist tracks")
	playlist, err := client.GetPlaylist(ctx, playlistId)
	if err != nil {
		slog.Error("Cannot get playlist", "id", playlistId)
		// The whole point is to manage playlists, so not much to continue here now
		signalCh <- syscall.SIGTERM
		return
	}

	for {
		slog.Info("Playlist received", "name", playlist.Name, "tracks", len(playlist.Tracks.Tracks), "total", playlist.Tracks.Total)
		for index, item := range playlist.Tracks.Tracks {
			slog.Info(fmt.Sprintf("    %02d: [%s] %s",
				index+1,
				item.Track.URI,
				formatTrack(item.Track.SimpleTrack),
			))
			addToMap(item.Track.SimpleTrack)
		}

		// see https://github.com/zmb3/spotify/blob/master/examples/paging/page.go
		err = client.NextPage(ctx, &playlist.Tracks)
		if err != nil {
			if !errors.Is(err, spotify.ErrNoMorePages) {
				slog.Error("Error while retrieving playlist track page", "err", err)
			}
			break
		}
	}
}

func addToMap(track spotify.SimpleTrack) bool {
	_, found := songsMap[track.URI]
	if !found {
		songsMap[track.URI] = formatTrack(track)
		slog.Debug("Adding new song to map", "uri", track.URI, "newSize", len(songsMap))
	}
	return !found
}

func getSongMap(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(writer).Encode(songsMap)
	if err != nil {
		slog.Warn("Failed to encode songs map", "err", err)
		http.Error(writer, "Failed to encode to JSON", http.StatusInternalServerError)
		return
	}
}

func updatePlaylist(ctx context.Context, client *spotify.Client, playlistId spotify.ID, newSongs []spotify.ID) {
	if len(newSongs) == 0 {
		slog.Debug("Updating playlist skipped, no new songs")
		return
	}

	// reverse list to add in the order they were played
	slices.Reverse(newSongs)
	slog.Debug("Updating playlist", "newSongsCount", len(newSongs), "songIds", newSongs)
	snapshot, err := client.AddTracksToPlaylist(ctx, playlistId, newSongs...)
	if err != nil {
		slog.Warn("Failed to update playlist", "err", err)
		return
	}
	slog.Debug("Playlist updated", "snapshot ID", snapshot)
}
