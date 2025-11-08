package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/zmb3/spotify/v2"
)

const httpHandlersHost = ""
const httpHandlersPort = 58071

func HandlersSetup() {
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("GET /callback", handleSpotifyCallback)
	http.HandleFunc("GET /{$}", handleIndex)
	http.HandleFunc("GET /playlists", handleGetPlaylists)
	http.HandleFunc("GET /playlist-active", handleGetPlaylistActive)
	http.HandleFunc("GET /playlist-select", handleGetPlaylistSelect)
	http.HandleFunc("POST /playlist-select/{id}", handlePostPlaylistSelect)
	http.HandleFunc("GET /now", handleGetNow)
	http.HandleFunc("GET /songmap", handleGetSongMap)
	http.HandleFunc("GET /last", handleGetLast)
	http.HandleFunc("GET /last/{count}", handleGetLast)
	http.HandleFunc("GET /last-before/{timestamp}", handleGetLastBefore)
	http.HandleFunc("GET /last-before/{timestamp}/{count}", handleGetLastBefore)
	http.HandleFunc("GET /last-after/{timestamp}", handleGetLastAfter)
	http.HandleFunc("GET /last-after/{timestamp}/{count}", handleGetLastAfter)
	http.HandleFunc("GET /startlist", handleGetStartList)
	http.HandleFunc("GET /runstate", handleGetRunState)
	http.HandleFunc("POST /start", handlePostStart)
	http.HandleFunc("POST /start/{timestamp}", handlePostStart)
	http.HandleFunc("POST /stop", handlePostStop)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Request not found", "method", r.Method, "url", r.URL.String())
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Not Found"))
	})
}

func HandlersRun() {
	listenAddress := fmt.Sprintf("%s:%d", httpHandlersHost, httpHandlersPort)
	log.Fatal(http.ListenAndServe(listenAddress, nil))
}

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

	slog.Debug("Saving token", "file", SpotifyTokenCacheFile)
	if err := SaveToken(token); err != nil {
		slog.Warn("Failed to save token", "err", err)
	}

	tokenCh <- token
	writer.Header().Set("Refresh", "3; url=/")
	if err := renderTemplate(writer, "templates/authdone.html", nil); err != nil {
		http.Error(writer, "Something went wrong", http.StatusInternalServerError)
		return
	}
}

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

func handleGetSongMap(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(songsMap); err != nil {
		slog.Warn("Failed to encode songs map", "err", err)
		http.Error(writer, "Failed to encode to JSON", http.StatusInternalServerError)
		return
	}
}

func handleGetPlaylists(writer http.ResponseWriter, _ *http.Request) {
	ctx := context.Background()
	items, err := GetPlaylists(ctx)
	if err != nil {
		slog.Warn("Failed to get playlists", "err", err)
		http.Error(writer, "Failed to get playlists", http.StatusInternalServerError)
		return
	}
	data := struct{ Playlists []PlaylistInfo }{Playlists: items}
	if err := renderTemplate(writer, "templates/snippets/playlists.html", data); err != nil {
		return
	}
}

func handleGetPlaylistActive(writer http.ResponseWriter, _ *http.Request) {
	playlistName := playlist.Name
	data := struct{ Playlist string }{Playlist: playlistName}
	if err := renderTemplate(writer, "templates/snippets/playlist-active.html", data); err != nil {
		return
	}
}

func handleGetPlaylistSelect(writer http.ResponseWriter, _ *http.Request) {
	ctx := context.Background()
	items, err := GetPlaylists(ctx)
	if err != nil {
		slog.Warn("Failed to get playlists", "err", err)
		http.Error(writer, "Failed to get playlists", http.StatusInternalServerError)
		return
	}
	data := struct{ Playlists []PlaylistInfo }{Playlists: items}
	if err := renderTemplate(writer, "templates/snippets/playlist-select.html", data); err != nil {
		return
	}
}

func handlePostPlaylistSelect(writer http.ResponseWriter, request *http.Request) {
	ctx := context.Background()
	playlistId := spotify.ID(request.PathValue("id"))

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

	data := struct{ Playlist string }{Playlist: playlist.Name}
	if err := renderTemplate(writer, "templates/snippets/playlist-active.html", data); err != nil {
		return
	}
}

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

func handleGetLast(writer http.ResponseWriter, request *http.Request) {
	count := parseParamUint(request, "count", 5)
	handleLast(writer, int(count), 0)
}

func handleGetLastBefore(writer http.ResponseWriter, request *http.Request) {
	timestamp := parseParamUint(request, "timestamp", 0)
	count := parseParamUint(request, "count", 50)
	handleLast(writer, int(count), -timestamp)
}

func handleGetLastAfter(writer http.ResponseWriter, request *http.Request) {
	timestamp := parseParamUint(request, "timestamp", 0)
	count := parseParamUint(request, "count", 50)
	handleLast(writer, int(count), timestamp)
}

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

func handleGetRunState(writer http.ResponseWriter, _ *http.Request) {
	renderSpotifyRunState(writer, running)
}

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

func handlePostStop(writer http.ResponseWriter, _ *http.Request) {
	if !running {
		http.Error(writer, "Already stopped", http.StatusBadRequest)
		return
	}
	stopCh <- struct{}{}
	renderSpotifyRunState(writer, false)
}

func renderSpotifyRunState(writer http.ResponseWriter, state bool) {
	data := struct{ Running bool }{Running: state}
	if err := renderTemplate(writer, "templates/snippets/startstop.html", data); err != nil {
		return
	}
}
