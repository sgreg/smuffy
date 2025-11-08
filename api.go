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
	http.HandleFunc("GET /last", handleGetLast)
	http.HandleFunc("GET /last/{count}", handleGetLast)
	http.HandleFunc("GET /last-before/{timestamp}", handleGetLastBefore)
	http.HandleFunc("GET /last-before/{timestamp}/{count}", handleGetLastBefore)
	http.HandleFunc("GET /last-after/{timestamp}", handleGetLastAfter)
	http.HandleFunc("GET /last-after/{timestamp}/{count}", handleGetLastAfter)
	http.HandleFunc("POST /start", handlePostStart)
	http.HandleFunc("POST /stop", handlePostStop)
	http.HandleFunc("/toggle", handleToggle)
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
	data := struct {
		Username string
		Running  bool
	}{
		Username: username,
		Running:  running,
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

// can't decide yet which way to go, /start and /stop or just /toggle, or maybe even finding use for both

func handlePostStart(writer http.ResponseWriter, _ *http.Request) {
	if running {
		http.Error(writer, "Already started", http.StatusBadRequest)
		return
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

func handleToggle(writer http.ResponseWriter, request *http.Request) {
	var runningState = running

	if request.Method == "POST" {
		if running {
			stopCh <- struct{}{}
		} else {
			startCh <- struct{}{}
		}
		// "running" value itself is updated on the channels' receiving ends, so inverting it manually here
		runningState = !runningState

	} else if request.Method != "GET" {
		http.Error(writer, "Not Found", http.StatusNotFound)
	}

	renderSpotifyRunState(writer, runningState)
}

func renderSpotifyRunState(writer http.ResponseWriter, state bool) {
	data := struct{ Running bool }{Running: state}
	if err := renderTemplate(writer, "templates/snippets/startstop.html", data); err != nil {
		return
	}
}
