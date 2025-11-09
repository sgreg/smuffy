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

type PlaylistInfo struct {
	Name string
	ID   spotify.ID
}

var (
	auth     *spotifyauth.Authenticator
	client   *spotify.Client
	playlist PlaylistInfo
	songsMap map[spotify.ID]string
	tokenCh        = make(chan *oauth2.Token)
	username       = ""
	userId         = ""
	authUrl        = ""
	state          = "abc123"
	running        = false
	startCh        = make(chan struct{})
	stopCh         = make(chan struct{})
	lastTime int64 = 0
)

// Time offset when updating lastTime after processing a playlist, just in case
const checkTimeBufferMs = 60 * 1000 // 1 Minute

// createSpotifyAuth returns a spotifyauth.Authenticator object set up with all required scopes.
func createSpotifyAuth() *spotifyauth.Authenticator {
	redirectUri := GetEnvString("SPOTIFY_REDIRECT_URL", "http://localhost:12345/callback")

	return spotifyauth.New(
		spotifyauth.WithRedirectURL(redirectUri),
		spotifyauth.WithScopes(
			spotifyauth.ScopeUserReadPrivate,
			spotifyauth.ScopeUserReadCurrentlyPlaying,
			spotifyauth.ScopeUserReadRecentlyPlayed,
			spotifyauth.ScopePlaylistReadPrivate,
			spotifyauth.ScopePlaylistModifyPublic,
			spotifyauth.ScopePlaylistModifyPrivate,
		),
	)
}

// SpotifyClientSetup initializes authentication with the Spotify API, creates the spotify.Client object,
// and gathers the active Spotify user's information.
//
// If a token is cached, it tries to load and use it, otherwise it waits for data to read on the tokenCh
// channel sent by the handleSpotifyCallback http handler on successful API authentication.
func SpotifyClientSetup(autostartEnabled bool) {
	running = autostartEnabled
	auth = createSpotifyAuth()
	slog.Debug("Trying to load cached token")
	token, err := LoadToken()
	if err != nil {
		slog.Info("Token not found, or unable to parse, auth required")
		authUrl = auth.AuthURL(state)
		fmt.Println("Please log in to Spotify by visiting the following page in your browser:", authUrl)
		// wait for auth to complete
		token = <-tokenCh
	} else {
		slog.Debug("Cached token found")
	}

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
	userId = user.ID
	authUrl = ""
}

// SpotifyPlaylistHandlerRun starts and loops the Spotify playlist processing.
func SpotifyPlaylistHandlerRun(skipActivateDefaultPlaylist bool) {
	playlistId := spotify.ID(GetEnvString("SPOTIFY_DEFAULT_PLAYLIST_ID", ""))
	playlistRequestIntervalMinutes := GetEnvInt("PLAYLIST_REQUEST_INTERVAL_MINUTES", 10)
	slog.Debug("Setting up Spotify Playlist Dumper")
	waitDuration := time.Duration(playlistRequestIntervalMinutes) * time.Minute

	ctx := context.Background()

	if skipActivateDefaultPlaylist {
		slog.Info("Skipping default playlist activation, make sure to select one via web UI")
	} else if playlistId == "" {
		slog.Warn("No default playlist defined, make sure to select one via web UI")
	} else {
		slog.Debug("Default playlist selected", "id", playlistId)
		ActivatePlaylist(ctx, client, playlistId)
	}

	for {
		waitForStart()
		handlePlaylistLoop(ctx, waitDuration)
	}
}

// waitForStart blocks until it reads data from the startCh channel.
// If a playlist is activated when data is received on the channel, running is set to true,
// and the function returns, otherwise the function keeps waiting.
func waitForStart() {
	for !running {
		slog.Debug("Waiting for things to actually start")
		<-startCh
		if playlist.ID == "" {
			slog.Error("Start triggered but no playlist selected, not doing anything")
		} else {
			running = true
		}
	}
	slog.Debug("Things are starting NOW!")
}

// handlePlaylistLoop runs the loop for the periodic playlist processing.
// Calls processPlaylist and waits either for the defined wait duration to run again,
// or for data on the stopCh channel, in which case it does one last call to processPlaylist
// and terminates.
func handlePlaylistLoop(ctx context.Context, waitDuration time.Duration) {
	for {
		processPlaylist(ctx)

		select {
		case <-stopCh:
			slog.Debug("stopCh triggered")
			running = false
			slog.Debug("Processing one last time the playlist")
			processPlaylist(ctx)
			return

		case <-time.After(waitDuration):
			slog.Debug("Periodic playlist processing triggered")
		}
	}
}

// processPlaylist retrieves the list of songs played since the last request from the Spotify API
// and checks for each of them if they already exist in the songsMap. If yes, the song is ignored,
// otherwise it collects it to a second list and adds them to the currently active playlist.
func processPlaylist(ctx context.Context) {
	// my function naming game is going very well right now ...

	playing, err := GetCurrentlyPlaying(ctx)
	if err != nil {
		slog.Error("Cannot get what's playing now", "err", err)
	} else {
		slog.Info("Playing: " + playing)
	}

	songs, err := GetLastSongs(ctx, 50, lastTime)
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
		updatePlaylist(ctx, client, newSongs)
		lastTime = time.Now().UnixMilli() - checkTimeBufferMs
	}
}

// formatTrack returns a "Artist - Name" formatted string of the given track data.
func formatTrack(track spotify.SimpleTrack) string {
	return fmt.Sprintf("%s - %s", track.Artists[0].Name, track.Name)
}

// ActivatePlaylist sets the given playlistId as the new active playlist, and all recently played songs
// are from now on added to that list. Receives all existing songs on the playlist via Spotify API,
// and populates the internal songsMap with it.
//
// If the given playlistId doesn't exist or can't be retrieved for other reasons, the whole program
// will be terminated (although this should be changed and simply show an error instead)
func ActivatePlaylist(ctx context.Context, client *spotify.Client, playlistId spotify.ID) {
	slog.Debug("Activating playlist", "id", playlistId)
	plist, err := client.GetPlaylist(ctx, playlistId)
	if err != nil {
		slog.Error("Cannot get playlist", "id", plist)
		// The whole point is to manage playlists, so not much to continue here now
		// TODO terminating isn't really necessary anymore, now that playlists can be selected on the go
		signalCh <- syscall.SIGTERM
		return
	}

	playlist.ID = plist.ID
	playlist.Name = plist.Name
	songsMap = make(map[spotify.ID]string)

	for {
		slog.Info("Playlist received", "name", plist.Name, "tracks", len(plist.Tracks.Tracks), "total", plist.Tracks.Total)
		for index, item := range plist.Tracks.Tracks {
			slog.Info(fmt.Sprintf("    %02d: [%s] %s",
				index+1,
				item.Track.ID,
				formatTrack(item.Track.SimpleTrack),
			))
			addToMap(item.Track.SimpleTrack)
		}

		// see https://github.com/zmb3/spotify/blob/master/examples/paging/page.go
		err = client.NextPage(ctx, &plist.Tracks)
		if err != nil {
			if !errors.Is(err, spotify.ErrNoMorePages) {
				slog.Error("Error while retrieving playlist track page", "err", err)
			}
			break
		}
	}

	slog.Debug("Playlist activated", "id", playlist.ID, "name", playlist.Name)
}

// addToMap checks if the given track doesn't exist in the songsMap yet and adds it to it.
// Returns true if the song was added, and false if the song already existed.
func addToMap(track spotify.SimpleTrack) bool {
	_, found := songsMap[track.ID]
	if !found {
		songsMap[track.ID] = formatTrack(track)
		slog.Debug("Adding new song to map", "id", track.ID, "newSize", len(songsMap))
	}
	return !found
}

// updatePlaylist adds the list of newSongs to the currently active playlist.
// Songs are added in the order they were played.
func updatePlaylist(ctx context.Context, client *spotify.Client, newSongs []spotify.ID) {
	if len(newSongs) == 0 {
		slog.Debug("Updating playlist skipped, no new songs")
		return
	}

	// reverse list to add songs in the order they were played
	slices.Reverse(newSongs)
	slog.Debug("Updating playlist", "newSongsCount", len(newSongs), "songIds", newSongs)
	snapshot, err := client.AddTracksToPlaylist(ctx, playlist.ID, newSongs...)
	if err != nil {
		slog.Warn("Failed to update playlist", "err", err)
		return
	}
	slog.Debug("Playlist updated", "snapshot ID", snapshot)
}

// GetPlaylists retrieves and returns the list of the active Spotify user's playlists.
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
		items = append(items, PlaylistInfo{Name: p.Name, ID: p.ID})
	}
	return items, nil
}

// ValidatePlaylist checks if a given playlist id is valid for the playlist processing.
// If first tries to retrieve it from the Spotify API and ensures the current active
// Spotify user is the owner of that playlist.
//
// Returns true if retrieving the list succeeds and the ownership matches, false otherwise.
func ValidatePlaylist(ctx context.Context, id spotify.ID) bool {
	plist, err := client.GetPlaylist(ctx, id)
	if err != nil {
		slog.Warn("Failed to get playlist", "id", id, "err", err)
		return false
	}

	slog.Info("Playlist found", "id", id, "name", plist.Name, "owner", plist.Owner.ID)

	// Could also consider to check ` || plist.Collaborative`, but let's only care for our own playlists for now
	return plist.Owner.ID == userId
}

// GetCurrentlyPlaying retrieves and returns the name of the artist and song that is currently
// being played on Spotify. If playback is paused, a literal "nothing" string is returned instead.
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
