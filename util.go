package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"

	"golang.org/x/oauth2"
)

const SpotifyTokenCacheFile = ".cache"

// SaveToken writes the given token data to the file system to reuse it on future application starts,
// without requesting it again from the Spotify authentication API.
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

// LoadToken reads a previously saved token back from the file system and returns it.
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

// GetEnvString returns the environment variable value of a given key as a string.
// If no such environment variable exists, the fallback value is returned instead.
func GetEnvString(key string, fallback string) string {
	if value, found := os.LookupEnv(key); found {
		return value
	}
	return fallback
}

// GetEnvInt returns the environment variable value of a given key as an integer.
// If no such environment variable exists, or converting its value to an integer fails,
// the fallback value is returned instead.
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
