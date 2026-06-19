package main

import (
	"encoding/json"
	"fmt"
)

// Game is one configured minigame type: which image to run and how many warm
// pods to keep ready. Loaded from the minigames ConfigMap (JSON).
type Game struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	PoolSize int    `json:"poolSize"`
}

// parseGames decodes the games config JSON. An empty list is rejected — a
// controller with zero games is always a misconfiguration.
func parseGames(b []byte) ([]Game, error) {
	var games []Game
	if err := json.Unmarshal(b, &games); err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("games config is empty")
	}
	return games, nil
}

// findGame returns the configured game by name, or nil if not configured.
func findGame(games []Game, name string) *Game {
	for i := range games {
		if games[i].Name == name {
			return &games[i]
		}
	}
	return nil
}
