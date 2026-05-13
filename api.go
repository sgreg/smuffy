package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/zmb3/spotify/v2"
)

//go:embed static templates
var webUiContent embed.FS

const httpHandlersHost = ""
const httpHandlersPort = 58071

type ApiService struct {
	spotify *SpotifyService
	mux     *http.ServeMux
}

func NewApiService(spotifyService *SpotifyService) *ApiService {
	return &ApiService{
		spotify: spotifyService,
		mux:     http.NewServeMux(),
	}
}

// Setup initializes all the http handlers.
func (a *ApiService) Setup() {
	a.mux.Handle("/static/", http.FileServer(http.FS(webUiContent)))

	a.mux.HandleFunc("GET /callback", a.handleSpotifyCallback)
	a.mux.HandleFunc("GET /{$}", a.handleIndex)
	a.mux.HandleFunc("GET /playlist-select", a.handleGetPlaylistSelect)
	a.mux.HandleFunc("POST /playlist-select/{id}", a.handlePostPlaylistSelect)
	a.mux.HandleFunc("GET /playing", a.handleGetPlaying)
	a.mux.HandleFunc("GET /playing/{count}", a.handleGetPlaying)
	a.mux.HandleFunc("GET /startlist", a.handleGetStartList)
	a.mux.HandleFunc("GET /runstate", a.handleGetRunState)
	a.mux.HandleFunc("POST /start", a.handlePostStart)
	a.mux.HandleFunc("POST /start/now", a.handlePostStartNow)
	a.mux.HandleFunc("POST /start/{timestamp}", a.handlePostStart)
	a.mux.HandleFunc("POST /stop", a.handlePostStop)
	a.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Request not found", "method", r.Method, "url", r.URL.String())
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Not Found"))
	})
}

// Run starts and loops the http server.
func (a *ApiService) Run() {
	listenAddress := fmt.Sprintf("%s:%d", httpHandlersHost, httpHandlersPort)
	log.Fatal(http.ListenAndServe(listenAddress, a.mux))
}

// renderTemplate takes the given templateFile and data and renders it to the given ResponseWriter.
// If loading the template or rendering itself fails, http.StatusInternalServerError is returned.
func (a *ApiService) renderTemplate(w http.ResponseWriter, templateFile string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, err := template.ParseFS(webUiContent, templateFile)
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
func (a *ApiService) handleSpotifyCallback(writer http.ResponseWriter, request *http.Request) {
	token, err := a.spotify.auth.Token(request.Context(), a.spotify.state, request)
	if err != nil {
		http.Error(writer, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}
	if st := request.FormValue("state"); st != a.spotify.state {
		http.NotFound(writer, request)
		log.Fatalf("State mismatch: %s != %s\n", st, a.spotify.state)
	}

	if GetEnvInt("CACHE_AUTH_TOKEN", 0) == 1 {
		slog.Debug("Saving token", "file", SpotifyTokenCacheFile)
		if err := SaveToken(token); err != nil {
			slog.Warn("Failed to save token", "err", err)
		}
	}

	a.spotify.tokenCh <- token
	writer.Header().Set("Refresh", "3; url=/")
	if err := a.renderTemplate(writer, "templates/authdone.html", nil); err != nil {
		http.Error(writer, "Something went wrong", http.StatusInternalServerError)
		return
	}
}

// handleIndex http handler renders the home page on index.html.
func (a *ApiService) handleIndex(writer http.ResponseWriter, _ *http.Request) {
	data := struct {
		Username string
		AuthUrl  string
	}{
		Username: a.spotify.username,
		AuthUrl:  a.spotify.authURL,
	}
	if err := a.renderTemplate(writer, "templates/index.html", data); err != nil {
		return
	}
}

// handleGetPlaylistSelect http handler retrieves and renders the list of playlists for the active Spotify user,
// intended to select a new playlist to populate from it.
func (a *ApiService) handleGetPlaylistSelect(writer http.ResponseWriter, _ *http.Request) {
	ctx := context.Background()
	items, err := a.spotify.GetPlaylists(ctx)
	if err != nil {
		slog.Warn("Failed to get playlists", "err", err)
		http.Error(writer, "Failed to get playlists", http.StatusInternalServerError)
		return
	}
	data := struct {
		Activated PlaylistInfo
		Playlists []PlaylistInfo
	}{
		Activated: a.spotify.playlist,
		Playlists: items,
	}

	if err := a.renderTemplate(writer, "templates/snippets/playlists.html", data); err != nil {
		return
	}
}

// handlePostPlaylistSelect http handler sets up the selected playlist as the new active to one to populate
// with the recently played songs, assuming it's valid and the current user can modify.
//
// If the new playlist is the same as the already active playlist, the request is more or less ignored.
// If playlist handling is already ongoing, it's stopped first.
//
// Renders the new state of active playlist on success.
func (a *ApiService) handlePostPlaylistSelect(writer http.ResponseWriter, request *http.Request) {
	playlistId := spotify.ID(request.PathValue("id"))

	if playlistId == a.spotify.playlist.ID {
		slog.Info("Trying to activate already activated playlist, ignoring", "id", playlistId)
		http.Error(writer, "Playlist already active", http.StatusNoContent)
		return
	}

	ctx := context.Background()
	if !a.spotify.ValidatePlaylist(ctx, playlistId) {
		slog.Warn("Playlist validation failed", "id", playlistId)
		http.Error(writer, "Failed to get playlist", http.StatusNotFound)
		return
	}

	if a.spotify.running {
		slog.Info("Shutting down current playlist handling", "id", a.spotify.playlist.ID, "name", a.spotify.playlist.Name)
		a.spotify.stopCh <- struct{}{}
	}

	a.spotify.ActivatePlaylist(ctx, playlistId)

	a.renderSpotifyRunState(writer, a.spotify.running)
}

// handleGetPlaying http handler renders the currently playing song and play history.
// An optional 'count' parameter can be added to the url to limit the number of maximum songs to show,
// defaulting to 0 if omitted.
//
// Rendering itself will add a "load more" option if the 'count' parameter is below Spotify's maximum
// number of last played songs (50).
func (a *ApiService) handleGetPlaying(writer http.ResponseWriter, request *http.Request) {
	ctx := context.Background()
	now, err := a.spotify.GetCurrentlyPlaying(ctx)
	if err != nil {
		slog.Warn("Failed to get currently playing", "err", err)
		http.Error(writer, "Failed to get currently playing", http.StatusInternalServerError)
		return
	}

	count := int(a.parseParamUint(request, "count", 10))
	songs, err := a.spotify.GetLastSongs(context.Background(), count, 0)
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

	if err := a.renderTemplate(writer, "templates/snippets/playing.html", data); err != nil {
		return
	}
}

// parseParamUint tries to get the value for the request's path wildcard parameter, expecting an unsigned int,
// and return it. If the paramName doesn't exist in the request path or fails to render, the given 'defaultValue'
// is used as a fallback instead.
//
// Note that despite parsing an unsinged integer, a signed integer value is returned here, since any place further
// down the road wants a signed integer anyway, and neither of the path values should be negative.
func (a *ApiService) parseParamUint(request *http.Request, paramName string, defaultValue int64) int64 {
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

// handleGetStartList http handler retrieves and renders the list of all recently played songs,
// intended to select a starting point for populating songs into the activated playlist.
func (a *ApiService) handleGetStartList(writer http.ResponseWriter, _ *http.Request) {
	songs, err := a.spotify.GetLastSongs(context.Background(), 50, 0)
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
	if err := a.renderTemplate(writer, "templates/snippets/startlist.html", data); err != nil {
		return
	}
}

// handleGetRunState http handler renders the current playlist processing run state.
func (a *ApiService) handleGetRunState(writer http.ResponseWriter, _ *http.Request) {
	a.renderSpotifyRunState(writer, a.spotify.running)
}

// handlePostStart http handler tries to start the playlist processing by writing to the startCh channel.
//
// If processing is already running, http.StatusBadRequest is returned.
// If no active playlist is selected, http.StatusBadRequest is returned as well, as processing requires one.
//
// If a timestamp parameter is given in the request url path, only songs after that time will be added to the
// playlist, otherwise all the last played songs will be used.
func (a *ApiService) handlePostStart(writer http.ResponseWriter, request *http.Request) {
	if a.spotify.running {
		http.Error(writer, "Already started", http.StatusBadRequest)
		return
	}
	if a.spotify.playlist.ID == "" {
		// TODO show some error on the UI somewhere in that case
		slog.Warn("Start requested but no playlist selected")
		http.Error(writer, "No playlist selected", http.StatusBadRequest)
		return
	}
	timestamp := a.parseParamUint(request, "timestamp", 0)
	if timestamp > 0 {
		a.spotify.lastTime = timestamp
	}
	a.spotify.startCh <- struct{}{}
	a.renderSpotifyRunState(writer, true)
}

// handlePostStartNow http handler tries to start the playlist processing by writing to the startCh channel.
// If playback is currently ongoing, the start time of that song is taken as the processing starting point,
// otherwise the current time is taken, and processing will essentially start with the next played song.
//
// If processing is already running, http.StatusBadRequest is returned.
// If no active playlist is selected, http.StatusBadRequest is returned as well, as processing requires one.
func (a *ApiService) handlePostStartNow(writer http.ResponseWriter, _ *http.Request) {
	if a.spotify.running {
		http.Error(writer, "Already started", http.StatusBadRequest)
		return
	}
	if a.spotify.playlist.ID == "" {
		// TODO show some error on the UI somewhere in that case
		slog.Warn("Start requested but no playlist selected")
		http.Error(writer, "No playlist selected", http.StatusBadRequest)
		return
	}
	track, err := a.spotify.GetCurrentlyPlayedTrack(context.Background())
	if err != nil {
		slog.Warn("Cannot get currently played track", "err", err)
		http.Error(writer, "Failed to get currently played track", http.StatusInternalServerError)
		return
	}

	if track.Playing {
		slog.Debug("Track is playing, using its timestamp", "timestamp", track.Timestamp)
		a.spotify.lastTime = track.Timestamp
	} else {
		slog.Debug("Track not playing, using current time")
		a.spotify.lastTime = time.Now().UnixMilli()
	}

	a.spotify.startCh <- struct{}{}
	a.renderSpotifyRunState(writer, true)
}

// handlePostStop http handler tries to stop the playlist processing by writing to the stopCh channel.
//
// If processing is not running, http.StatusBadRequest is returned.
func (a *ApiService) handlePostStop(writer http.ResponseWriter, _ *http.Request) {
	if !a.spotify.running {
		http.Error(writer, "Already stopped", http.StatusBadRequest)
		return
	}
	a.spotify.stopCh <- struct{}{}
	a.renderSpotifyRunState(writer, false)
}

// renderSpotifyRunState renders the current playlist processing running state.
func (a *ApiService) renderSpotifyRunState(writer http.ResponseWriter, state bool) {
	data := struct {
		Running  bool
		Playlist PlaylistInfo
	}{
		Running:  state,
		Playlist: a.spotify.playlist,
	}
	if err := a.renderTemplate(writer, "templates/snippets/runstate.html", data); err != nil {
		return
	}
}
