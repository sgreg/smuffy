package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"

	"golang.org/x/oauth2"
)

const SpotifyTokenCacheFile = ".cache"

func SaveToken(token *oauth2.Token) error {
	file, err := os.Create(SpotifyTokenCacheFile)
	if err != nil {
		return err
	}
	// 'defer file.Close()' needs error handling, but not a lot of handling we can (or care to) do here
	defer func(file *os.File) { _ = file.Close() }(file)

	encoder := json.NewEncoder(file)
	if err = encoder.Encode(token); err != nil {
		return err
	}
	return nil
}

func LoadToken() (*oauth2.Token, error) {
	file, err := os.Open(SpotifyTokenCacheFile)
	if err != nil {
		return nil, err
	}
	defer func(file *os.File) { _ = file.Close() }(file)

	var token oauth2.Token
	decoder := json.NewDecoder(file)
	if err = decoder.Decode(&token); err != nil {
		return nil, err
	}
	return &token, nil
}

func GetEnvString(key string, fallback string) string {
	if value, found := os.LookupEnv(key); found {
		return value
	}
	return fallback
}

func GetEnvInt(key string, fallback int) int {
	if value, found := os.LookupEnv(key); found {
		if parseInt, err := strconv.Atoi(value); err == nil {
			return parseInt
		} else {
			slog.Warn("Failed to parse env var key, using fallback", "key", key, "fallback", fallback)
		}
	}
	return fallback
}
