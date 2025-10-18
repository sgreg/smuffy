package main

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
)

const httpHandlersHost = ""
const httpHandlersPort = 58071

func HandlersSetup() {
	http.HandleFunc("/callback", handleSpotifyCallback)
	http.HandleFunc("/songmap", handleGetSongMap)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Got request for:", r.URL.String())
	})
}

func HandlersRun() {
	listenAddress := fmt.Sprintf("%s:%d", httpHandlersHost, httpHandlersPort)
	log.Fatal(http.ListenAndServe(listenAddress, nil))
}

func handleSpotifyCallback(w http.ResponseWriter, r *http.Request) {
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
	if err := SaveToken(token); err != nil {
		slog.Warn("Failed to save token", "err", err)
	}

	fmt.Fprintf(w, "Login Completed!")
	tokenCh <- token
}

func handleGetSongMap(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(songsMap); err != nil {
		slog.Warn("Failed to encode songs map", "err", err)
		http.Error(writer, "Failed to encode to JSON", http.StatusInternalServerError)
		return
	}
}
