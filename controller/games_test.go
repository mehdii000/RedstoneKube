package main

import "testing"

func TestParseGames(t *testing.T) {
	in := []byte(`[{"name":"stub","image":"mc/minigame-stub:dev","poolSize":1},
	               {"name":"parkour","image":"mc/minigame-parkour:dev","poolSize":2}]`)
	g, err := parseGames(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(g) != 2 {
		t.Fatalf("len = %d, want 2", len(g))
	}
	if g[1].Name != "parkour" || g[1].Image != "mc/minigame-parkour:dev" || g[1].PoolSize != 2 {
		t.Errorf("game[1] = %+v", g[1])
	}
}

func TestParseGamesRejectsEmpty(t *testing.T) {
	if _, err := parseGames([]byte(`[]`)); err == nil {
		t.Error("want error on empty games list")
	}
	if _, err := parseGames([]byte(`not json`)); err == nil {
		t.Error("want error on bad json")
	}
}

func TestFindGame(t *testing.T) {
	games := []Game{{Name: "stub"}, {Name: "parkour"}}
	if findGame(games, "parkour") == nil {
		t.Error("parkour should be found")
	}
	if findGame(games, "ghost") != nil {
		t.Error("ghost should not be found")
	}
}
