package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/zmb3/spotify/v2"
)

const httpHandlersHost = ""
const httpHandlersPort = 58071

// ApiHandlersSetup initializes all the http handlers
func ApiHandlersSetup() {
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("GET /callback", handleSpotifyCallback)
	http.HandleFunc("GET /{$}", handleIndex)
	http.HandleFunc("GET /playlists", handleGetPlaylists)
	http.HandleFunc("GET /playlist-active", handleGetPlaylistActive)
	http.HandleFunc("GET /playlist-select", handleGetPlaylistSelect)
	http.HandleFunc("POST /playlist-select/{id}", handlePostPlaylistSelect)
	http.HandleFunc("GET /now", handleGetNow)
	http.HandleFunc("GET /last", handleGetLast)
	http.HandleFunc("GET /playing", handleGetPlaying)
	http.HandleFunc("GET /playing/{count}", handleGetPlaying)
	http.HandleFunc("GET /last/{count}", handleGetLast)
	http.HandleFunc("GET /last-before/{timestamp}", handleGetLastBefore)
	http.HandleFunc("GET /last-before/{timestamp}/{count}", handleGetLastBefore)
	http.HandleFunc("GET /last-after/{timestamp}", handleGetLastAfter)
	http.HandleFunc("GET /last-after/{timestamp}/{count}", handleGetLastAfter)
	http.HandleFunc("GET /startlist", handleGetStartList)
	http.HandleFunc("GET /runstate", handleGetRunState)
	http.HandleFunc("POST /start", handlePostStart)
	http.HandleFunc("POST /start/now", handlePostStartNow)
	http.HandleFunc("POST /start/{timestamp}", handlePostStart)
	http.HandleFunc("POST /stop", handlePostStop)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Request not found", "method", r.Method, "url", r.URL.String())
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Not Found"))
	})
}

// ApiHandlersRun starts and loops the http server.
func ApiHandlersRun() {
	listenAddress := fmt.Sprintf("%s:%d", httpHandlersHost, httpHandlersPort)
	log.Fatal(http.ListenAndServe(listenAddress, nil))
}

// renderTemplate takes the given templateFile and data and renders it to the given ResponseWriter.
// If loading the template or rendering itself fails, http.StatusInternalServerError is returned.
func renderTemplate(w http.ResponseWriter, templateFile string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		slog.Warn("Failed to parse template", "path", templateFile, "err", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return err
	}
	if err := tmpl.Execute(w, data); err != nil {
		slog.Warn("Failed to execute template", "path", templateFile, "err", err)
		http.Error(w, "Render error", http.StatusInternalServerError)
		return err
	}
	return nil
}

// handleSpotifyCallback http handler is used to finalize the Spotify authentication process.
// See https://developer.spotify.com/documentation/web-api/tutorials/code-flow for details on that.
//
// On successful authentication, this will receive the API token used for future requests.
// The token is sent via tokenCh channel where SpotifyClientSetup is waiting to read from.
//
// If CACHE_AUTH_TOKEN env var is set to 1, the token is cached for future use, and the whole
// process via Spotify authentication is skipped on the next startup.
//
// Renders HTML template to show successful authentication and redirects back home.
func handleSpotifyCallback(writer http.ResponseWriter, request *http.Request) {
	token, err := auth.Token(request.Context(), state, request)
	if err != nil {
		http.Error(writer, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}
	if st := request.FormValue("state"); st != state {
		http.NotFound(writer, request)
		log.Fatalf("State mismatch: %s != %s\n", st, state)
	}

	if GetEnvInt("CACHE_AUTH_TOKEN", 0) == 1 {
		slog.Debug("Saving token", "file", SpotifyTokenCacheFile)
		if err := SaveToken(token); err != nil {
			slog.Warn("Failed to save token", "err", err)
		}
	}

	tokenCh <- token
	writer.Header().Set("Refresh", "3; url=/")
	if err := renderTemplate(writer, "templates/authdone.html", nil); err != nil {
		http.Error(writer, "Something went wrong", http.StatusInternalServerError)
		return
	}
}

// handleIndex http handler renders the home page on index.html
func handleIndex(writer http.ResponseWriter, _ *http.Request) {
	data := struct {
		Username string
		AuthUrl  string
	}{
		Username: username,
		AuthUrl:  authUrl,
	}
	if err := renderTemplate(writer, "templates/index.html", data); err != nil {
		return
	}
}

// renderPlaylists tries to retrieve the list of playlists for the active Spotify user and renders it.
// If selectable is true, it will use the 'playlist-select.html' template snippet, otherwise 'playlists.html'.
//
// If retrieving the playlists fails, http.StatusInternalServerError is returned instead
func renderPlaylists(writer http.ResponseWriter, selectable bool) {
	ctx := context.Background()
	items, err := GetPlaylists(ctx)
	if err != nil {
		slog.Warn("Failed to get playlists", "err", err)
		http.Error(writer, "Failed to get playlists", http.StatusInternalServerError)
		return
	}
	data := struct {
		Activated PlaylistInfo
		Playlists []PlaylistInfo
	}{
		Activated: playlist,
		Playlists: items,
	}

	var fileName string
	if selectable {
		fileName = "templates/snippets/playlist-select.html"
	} else {
		fileName = "templates/snippets/playlists.html"
	}

	if err := renderTemplate(writer, fileName, data); err != nil {
		return
	}
}

// handleGetPlaylists http handler retrieves and renders the list of playlists for the active Spotify user.
func handleGetPlaylists(writer http.ResponseWriter, _ *http.Request) {
	renderPlaylists(writer, false)
}

// handleGetPlaylistActive http handler renders the status and name of the active playlist if there is one.
func handleGetPlaylistActive(writer http.ResponseWriter, _ *http.Request) {
	// TODO remove this I guess, it's identical to getrunstate part (also rename startstop.html maybe to runstate or something)
	renderSpotifyRunState(writer, running)
}

// handleGetPlaylists http handler retrieves and renders the list of playlists for the active Spotify user,
// intended to select a new playlist to populate from it.
func handleGetPlaylistSelect(writer http.ResponseWriter, _ *http.Request) {
	renderPlaylists(writer, true)
}

// handlePostPlaylistSelect http handler sets up the selected playlist as the new active to one to populate
// with the recently played songs, assuming it's valid and the current user can modify.
//
// If the new playlist is the same as the already active playlist, the request is more or less ignored.
// If playlist handling is already ongoing, it's stopped first.
//
// Renders the new state of active playlist on success.
func handlePostPlaylistSelect(writer http.ResponseWriter, request *http.Request) {
	playlistId := spotify.ID(request.PathValue("id"))

	if playlistId == playlist.ID {
		slog.Info("Trying to activate already activated playlist, ignoring", "id", playlistId)
		http.Error(writer, "Playlist already active", http.StatusNoContent)
		return
	}

	ctx := context.Background()
	if !ValidatePlaylist(ctx, playlistId) {
		slog.Warn("Playlist validation failed", "id", playlistId)
		http.Error(writer, "Failed to get playlist", http.StatusNotFound)
		return
	}

	if running {
		slog.Info("Shutting down current playlist handling", "id", playlist.ID, "name", playlist.Name)
		stopCh <- struct{}{}
	}

	ActivatePlaylist(ctx, client, playlistId) // FIXME why is client always passed around, it's global anyway?

	renderSpotifyRunState(writer, running)
}

// handleGetPlaying http handler renders the currently playing song and play history.
// An optional 'count' parameter can be added to the url to limit the number of maximum songs to show,
// defaulting to 0 if omitted.
//
// Rendering itself will add a "load more" option if the 'count' parameter is below Spotify's maximum
// number of last played songs (50).
func handleGetPlaying(writer http.ResponseWriter, request *http.Request) {
	ctx := context.Background()
	now, err := GetCurrentlyPlaying(ctx)
	if err != nil {
		slog.Warn("Failed to get currently playing", "err", err)
		http.Error(writer, "Failed to get currently playing", http.StatusInternalServerError)
		return
	}

	count := int(parseParamUint(request, "count", 10))
	songs, err := GetLastSongs(context.Background(), count, 0)
	if err != nil {
		slog.Warn("Failed to get last played songs", "err", err)
		http.Error(writer, "Failed to get last played songs", http.StatusInternalServerError)
		return
	}

	var last []string
	for index, song := range songs {
		last = append(last, formatTrack(song.Track))
		slog.Info(fmt.Sprintf("    %02d: [%s](%d) %s",
			index+1,
			song.PlayedAt.Format("2006-01-02 15:04:05 -0700 MST"),
			song.PlayedAt.UnixMilli(),
			formatTrack(song.Track),
		))
	}

	nextCount := count + 10
	if nextCount > 50 {
		nextCount = 0
	}

	data := struct {
		Now       string
		Items     []string
		Count     int
		NextCount int
	}{
		Now:       now,
		Items:     last,
		Count:     count,
		NextCount: nextCount,
	}

	if err := renderTemplate(writer, "templates/snippets/playing.html", data); err != nil {
		return
	}
}

// handleGetNow http handler renders the currently played song if there is one.
func handleGetNow(writer http.ResponseWriter, _ *http.Request) {
	ctx := context.Background()
	now, err := GetCurrentlyPlaying(ctx)
	if err != nil {
		slog.Warn("Failed to get currently playing", "err", err)
		http.Error(writer, "Failed to get currently playing", http.StatusInternalServerError)
		return
	}
	data := struct{ Now string }{Now: now}
	if err := renderTemplate(writer, "templates/snippets/now.html", data); err != nil {
		return
	}
}

// handleGetLast http handler renders the list of last played songs.
// An optional 'count' parameter can be added to the url to limit the number of maximum songs to show,
// defaulting to 5 if omitted.
func handleGetLast(writer http.ResponseWriter, request *http.Request) {
	count := parseParamUint(request, "count", 5)
	handleLast(writer, int(count), 0)
}

// handleGetLastBefore http handler renders the list of songs played before a given timestamp.
// An optional 'count' parameter can be added to the url to limit the number of maximum songs to show,
// defaulting to Spotify's current max value of 50 if omitted.
func handleGetLastBefore(writer http.ResponseWriter, request *http.Request) {
	timestamp := parseParamUint(request, "timestamp", 0)
	count := parseParamUint(request, "count", 50)
	handleLast(writer, int(count), -timestamp)
}

// handleGetLastBefore http handler renders the list of songs played after a given timestamp.
// An optional 'count' parameter can be added to the url to limit the number of maximum songs to show,
// defaulting to Spotify's current max value of 50 if omitted.
func handleGetLastAfter(writer http.ResponseWriter, request *http.Request) {
	timestamp := parseParamUint(request, "timestamp", 0)
	count := parseParamUint(request, "count", 50)
	handleLast(writer, int(count), timestamp)
}

// parseParamUint tries to get the value for the request's path wildcard parameter, expecting an unsigned int,
// and return it. If the paramName doesn't exist in the request path or fails to render, the given 'defaultValue'
// is used as a fallback instead.
//
// Note that despite parsing an unsinged integer, a signed integer value is returned here, since any place further
// down the road wants a signed integer anyway, and neither of the path values should be negative.
func parseParamUint(request *http.Request, paramName string, defaultValue int64) int64 {
	var value = defaultValue
	param := request.PathValue(paramName)
	slog.Debug("Parsing path param", "name", paramName, "value", param)
	if param != "" {
		conv, err := strconv.ParseUint(param, 10, 64)
		if err != nil {
			slog.Warn("Failed to convert parameter, using default", "param", param, "err", err, "default", defaultValue)
		} else {
			value = int64(conv)
		}
	}
	return value
}

// handleLast tries to retrieve the list of 'count' last played songs and render it.
// If a positive timestamp is given, only songs after that timestamp are requested.
// If a negative timestamp is given, only songs before that timestamp are requested.
// If the timestamp is zero, no time information is considered.
func handleLast(writer http.ResponseWriter, count int, timestamp int64) {
	songs, err := GetLastSongs(context.Background(), count, timestamp)
	if err != nil {
		slog.Warn("Failed to get last played songs", "err", err)
		http.Error(writer, "Failed to get last played songs", http.StatusInternalServerError)
		return
	}

	var last []string
	for index, song := range songs {
		last = append(last, formatTrack(song.Track))
		slog.Info(fmt.Sprintf("    %02d: [%s](%d) %s",
			index+1,
			song.PlayedAt.Format("2006-01-02 15:04:05 -0700 MST"),
			song.PlayedAt.UnixMilli(),
			formatTrack(song.Track),
		))
	}
	data := struct{ Items []string }{Items: last}
	if err := renderTemplate(writer, "templates/snippets/last.html", data); err != nil {
		return
	}
}

// handleGetStartList http handler retrieves and renders the list of all recently played songs,
// intended to select a starting point for populating songs into the activated playlist.
func handleGetStartList(writer http.ResponseWriter, request *http.Request) {
	songs, err := GetLastSongs(context.Background(), 50, 0)
	if err != nil {
		slog.Warn("Failed to get last played songs", "err", err)
		http.Error(writer, "Failed to get last played songs", http.StatusInternalServerError)
		return
	}

	type lastSongData struct {
		Name      string
		Timestamp int64
	}

	var last []lastSongData
	for _, song := range songs {
		last = append(last, lastSongData{
			Name:      formatTrack(song.Track),
			Timestamp: song.PlayedAt.Unix() * 1000, // round down to zero the ms part as some buffer
		})
	}
	data := struct{ Items []lastSongData }{Items: last}
	if err := renderTemplate(writer, "templates/snippets/startlist.html", data); err != nil {
		return
	}
}

// handleGetRunState http handler renders the current playlist processing run state.
func handleGetRunState(writer http.ResponseWriter, _ *http.Request) {
	renderSpotifyRunState(writer, running)
}

// handlePostStart http handler tries to start the playlist processing by writing to the startCh channel.
//
// If processing is already running, http.StatusBadRequest is returned.
// If no active playlist is selected, http.StatusBadRequest is returned as well, as processing requires one.
//
// If a timestamp parameter is given in the request url path, only songs after that time will be added to the
// playlist, otherwise all the last played songs will be used.
func handlePostStart(writer http.ResponseWriter, request *http.Request) {
	if running {
		http.Error(writer, "Already started", http.StatusBadRequest)
		return
	}
	if playlist.ID == "" {
		// TODO show some error on the UI somewhere in that case
		slog.Warn("Start requested but no playlist selected")
		http.Error(writer, "No playlist selected", http.StatusBadRequest)
		return
	}
	timestamp := parseParamUint(request, "timestamp", 0)
	if timestamp > 0 {
		lastTime = timestamp
	}
	startCh <- struct{}{}
	renderSpotifyRunState(writer, true)
}

// handlePostStartNow http handler tries to start the playlist processing by writing to the startCh channel.
// If playback is currently ongoing, the start time of that song is taken as the processing starting point,
// otherwise the current time is taken, and processing will essentially start with the next played song.
//
// If processing is already running, http.StatusBadRequest is returned.
// If no active playlist is selected, http.StatusBadRequest is returned as well, as processing requires one.
func handlePostStartNow(writer http.ResponseWriter, request *http.Request) {
	if running {
		http.Error(writer, "Already started", http.StatusBadRequest)
		return
	}
	if playlist.ID == "" {
		// TODO show some error on the UI somewhere in that case
		slog.Warn("Start requested but no playlist selected")
		http.Error(writer, "No playlist selected", http.StatusBadRequest)
		return
	}
	track, err := GetCurrentlyPlayedTrack(context.Background())
	if err != nil {
		slog.Warn("Cannot get currently played track", "err", err)
		http.Error(writer, "Failed to get currently played track", http.StatusInternalServerError)
		return
	}

	if track.Playing {
		slog.Debug("Track is playing, using its timestamp", "timestamp", track.Timestamp)
		lastTime = track.Timestamp
	} else {
		slog.Debug("Track not playing, using current time")
		lastTime = time.Now().UnixMilli()
	}

	startCh <- struct{}{}
	renderSpotifyRunState(writer, true)
}

// handlePostStop http handler tries to stop the playlist processing by writing to the stopCh channel.
//
// If processing is not running, http.StatusBadRequest is returned.
func handlePostStop(writer http.ResponseWriter, _ *http.Request) {
	if !running {
		http.Error(writer, "Already stopped", http.StatusBadRequest)
		return
	}
	stopCh <- struct{}{}
	renderSpotifyRunState(writer, false)
}

// renderSpotifyRunState renders the current playlist processing running state.
func renderSpotifyRunState(writer http.ResponseWriter, state bool) {
	data := struct {
		Running  bool
		Playlist PlaylistInfo
	}{
		Running:  state,
		Playlist: playlist,
	}
	if err := renderTemplate(writer, "templates/snippets/startstop.html", data); err != nil {
		return
	}
}
