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
)

const httpHandlersHost = ""
const httpHandlersPort = 58071

func HandlersSetup() {
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("GET /callback", handleSpotifyCallback)
	http.HandleFunc("GET /{$}", handleIndex)
	http.HandleFunc("GET /playlists", handleGetPlaylists)
	http.HandleFunc("GET /now", handleGetNow)
	http.HandleFunc("GET /songmap", handleGetSongMap)
	http.HandleFunc("GET /last/{count...}", handleGetLast)
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

	fmt.Fprintf(writer, "Login Completed!")
	tokenCh <- token
}

func handleIndex(writer http.ResponseWriter, _ *http.Request) {
	data := struct{ Username string }{Username: username}
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
	var count = 5
	param := request.PathValue("count")
	slog.Debug("Path param", "val", param)
	if param != "" {
		conv, err := strconv.Atoi(param)
		if err != nil {
			slog.Warn("Failed to convert parameter, using default", "param", param, "err", err)
		} else {
			count = conv
		}
	}

	songs, err := GetLastSongs(context.Background(), count)
	if err != nil {
		slog.Warn("Failed to get last played songs", "err", err)
		http.Error(writer, "Failed to get last played songs", http.StatusInternalServerError)
		return
	}

	var last []string
	for _, song := range songs {
		last = append(last, formatTrack(song.Track))
	}
	data := struct{ Items []string }{Items: last}
	if err := renderTemplate(writer, "templates/snippets/last.html", data); err != nil {
		return
	}
}
