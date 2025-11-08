package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"slices"
	"syscall"
	"time"

	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"
)

var (
	auth     *spotifyauth.Authenticator
	client   *spotify.Client
	tokenCh  = make(chan *oauth2.Token)
	songsMap = make(map[spotify.ID]string)
	username = "guest"
	state    = "abc123"
	running  = false
	startCh  = make(chan struct{})
	stopCh   = make(chan struct{})
)

func createSpotifyAuth() *spotifyauth.Authenticator {
	redirectUri := GetEnvString("SPOTIFY_REDIRECT_URL", "http://localhost:12345/callback")

	return spotifyauth.New(
		spotifyauth.WithRedirectURL(redirectUri),
		spotifyauth.WithScopes(
			spotifyauth.ScopeUserReadPrivate,
			spotifyauth.ScopeUserReadCurrentlyPlaying,
			spotifyauth.ScopeUserReadRecentlyPlayed,
		),
	)
}

func SpotifyClientSetup(autostartEnabled bool) {
	running = autostartEnabled
	auth = createSpotifyAuth()
	slog.Debug("Trying to load cached token")
	token, err := LoadToken()
	if err != nil {
		slog.Info("Token not found, or unable to parse, auth required")
		url := auth.AuthURL(state)
		fmt.Println("Please log in to Spotify by visiting the following page in your browser:", url)
		// wait for auth to complete
		token = <-tokenCh
	} else {
		slog.Debug("Cached token found")
	}

	// TODO might wanna find a way to update this occasionally (there's probably a proper way to do that in oauth2 package)
	client = spotify.New(auth.Client(context.Background(), token))

	// use the client to make calls that require authorization
	user, err := client.CurrentUser(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	slog.Info(fmt.Sprintf("You are logged in as: %s", user.ID))
	if user.DisplayName != "" {
		username = user.DisplayName
	} else {
		username = user.ID
	}
}

func SpotifyPlaylistDump() {
	playlistId := spotify.ID(GetEnvString("SPOTIFY_PLAYLIST_ID", "invalidListId"))
	playlistRequestIntervalMinutes := GetEnvInt("PLAYLIST_REQUEST_INTERVAL_MINUTES", 10)
	slog.Debug("Setting up Spotify Playlist Dumper")
	waitDuration := time.Duration(playlistRequestIntervalMinutes) * time.Minute

	ctx := context.Background()

	loadPlaylistToMap(ctx, client, playlistId)

	for {
		waitForStart()
		handlePlaylistLoop(ctx, playlistId, waitDuration)
	}
}

func waitForStart() {
	if !running {
		slog.Debug("Waiting for things to actually start")
		<-startCh
		running = true
		slog.Debug("Things are starting NOW!")
	}
}

func handlePlaylistLoop(ctx context.Context, playlistId spotify.ID, waitDuration time.Duration) {
	for {
		processPlaylist(ctx, playlistId)

		select {
		case <-stopCh:
			slog.Debug("stopCh triggered")
			running = false
			slog.Debug("Processing one last time the playlist")
			processPlaylist(ctx, playlistId)
			return

		case <-time.After(waitDuration):
			slog.Debug("Periodic playlist processing triggered")
		}
	}
}

func processPlaylist(ctx context.Context, playlistId spotify.ID) {
	// my function naming game is going very well right now ...

	playing, err := GetCurrentlyPlaying(ctx)
	if err != nil {
		slog.Error("Cannot get what's playing now", "err", err)
	} else {
		slog.Info("Playing: " + playing)
	}

	// TODO try PlayerRecentlyPlayedOpt() with after set to timestamp of last check (and obvs store last check timestamp) ..and maybe experiment with UTC offset from local time? (nope, it just needs milliseconds and not seconds)
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
				item.Track.ID,
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
	_, found := songsMap[track.ID]
	if !found {
		songsMap[track.ID] = formatTrack(track)
		slog.Debug("Adding new song to map", "id", track.ID, "newSize", len(songsMap))
	}
	return !found
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

type PlaylistInfo struct {
	Name string
	ID   string
}

func GetPlaylists(ctx context.Context) ([]PlaylistInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("spotify client not initialized")
	}
	page, err := client.CurrentUsersPlaylists(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]PlaylistInfo, 0, len(page.Playlists))
	for _, p := range page.Playlists {
		items = append(items, PlaylistInfo{Name: p.Name, ID: p.ID.String()})
	}
	return items, nil
}

func GetCurrentlyPlaying(ctx context.Context) (string, error) {
	if client == nil {
		return "", fmt.Errorf("spotify client not initialized")
	}
	playing, err := client.PlayerCurrentlyPlaying(ctx)
	if err != nil {
		return "", err
	}
	if playing == nil || playing.Item == nil {
		return "nothing", nil
	}
	return formatTrack(playing.Item.SimpleTrack), nil
}

// GetLastSongs requests the last count played songs from the Spotify API.
//
// If the timestamp is set to 0, the current date and time are implied.
//
// If the timestamp is a positive value, the call requests all played songs after that timestamp.
// If the given count value is less than the total number of songs played since that timestamp,
// only the oldest count number of songs is returned.
//
// If the imestamp is a negative value, the call requests all played songs before that timestamp.
// If the given count value is less than the total number of songs played since that timestamp,
// only the newest count number of songs is returned.
func GetLastSongs(ctx context.Context, count int, timestamp int64) ([]spotify.RecentlyPlayedItem, error) {
	opts := spotify.RecentlyPlayedOptions{Limit: spotify.Numeric(count)}
	if timestamp > 0 {
		opts.AfterEpochMs = timestamp
	} else if timestamp < 0 {
		opts.BeforeEpochMs = -timestamp
	}
	return client.PlayerRecentlyPlayedOpt(ctx, &opts)
}
