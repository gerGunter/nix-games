package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSteamRequiresExplicitGameType(t *testing.T) {
	for _, s := range []string{`{}`, `{"response":{"store_items":[{"id":1,"appid":1,"success":1,"item_type":0,"name":"Missing type"}]}}`, `{"response":{"store_items":[{"id":1,"appid":1,"success":1,"type":0,"name":"Missing item type"}]}}`} {
		if _, err := parseSteam([]byte(s)); err == nil {
			t.Fatal("accepted missing schema", s)
		}
	}
}
func TestGameClassification(t *testing.T) {
	zero, six := 0, 6
	apps := []App{
		{ID: "game", Name: "Game", SKUs: []SKU{{"steam", " 730\t"}, {"steam", ""}}},
		{ID: "software", Name: "Software", SKUs: []SKU{{"steam", "431960"}}},
		{ID: "tool", Name: "SRPG Studio", SKUs: []SKU{{"steam", "857320"}}},
		{ID: "unknown", Name: "Unknown"},
		{ID: "invalid", Name: "Invalid link", SKUs: []SKU{{"steam", "$6.99"}}},
		{ID: "conflict", Name: "Conflicting links", SKUs: []SKU{{"steam", "730"}, {"steam", "431960"}}},
		{ID: "unavailable", Name: "Unavailable", SKUs: []SKU{{"steam", "42"}}},
	}
	data, _ := json.Marshal(apps)
	candidates := []Candidate{}
	for _, a := range apps {
		candidates = append(candidates, Candidate{Name: a.ID + ".exe", Evidence: []Evidence{{ID: a.ID, App: a.Name}}})
	}
	r := SteamResponse{}
	r.Response.Items = []SteamItem{{730, 730, 1, &zero, &zero, "CS2"}, {431960, 431960, 1, &zero, &six, "Wallpaper Engine"}, {857320, 857320, 1, &zero, &six, "SRPG Studio"}, {ID: 42, Success: 2}}
	snapshot, _ := json.Marshal(r)
	file := filepath.Join(t.TempDir(), "steam.json")
	os.WriteFile(file, snapshot, 0600)
	games, unknown, counts, _, err := classify(data, candidates, file, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || games[0].Name != "game.exe" || len(games[0].Games) != 1 {
		t.Fatalf("unexpected games: %+v", games)
	}
	if len(unknown) != 3 || counts["non_game_or_conflicting_type"] != 3 {
		t.Fatal(counts)
	}
}
