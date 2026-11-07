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
func TestXboxSchema(t *testing.T) {
	for _, s := range []string{`{}`, `{"Products":[{"ProductId":"bad","ProductKind":"Game"}]}`, `{"Products":[{"ProductId":"9NT3WC6RKBVQ"}]}`} {
		if _, err := parseXbox([]byte(s)); err == nil {
			t.Fatal("accepted malformed Microsoft Store data", s)
		}
	}
}
func TestGameClassification(t *testing.T) {
	zero, one, four, six := 0, 1, 4, 6
	apps := []App{
		{ID: "game", Name: "Game", SKUs: []SKU{{"steam", " 730\t"}, {"steam", ""}}},
		{ID: "software", Name: "Software", SKUs: []SKU{{"steam", "431960"}}},
		{ID: "tool", Name: "SRPG Studio", SKUs: []SKU{{"steam", "857320"}}},
		{ID: "unknown", Name: "Unknown"},
		{ID: "invalid", Name: "Invalid link", SKUs: []SKU{{"steam", "$6.99"}}},
		{ID: "conflict", Name: "Conflicting links", SKUs: []SKU{{"steam", "730"}, {"steam", "431960"}}},
		{ID: "unavailable", Name: "Unavailable", SKUs: []SKU{{"steam", "42"}}},
		// A game linked with its DLC and a delisted SKU is still a game (Darktide).
		{ID: "dlc", Name: "Game with DLC", SKUs: []SKU{{"steam", "100"}, {"steam", "101"}, {"steam", "42"}}},
		{ID: "demo", Name: "Game Demo", SKUs: []SKU{{"steam", "102"}}},
		{ID: "xgame", Name: "Store game", SKUs: []SKU{{"xbox", "9NT3WC6RKBVQ"}}},
		{ID: "xapp", Name: "Store app", SKUs: []SKU{{"xbox", "9WZDNCRFJ3TJ"}}},
	}
	data, _ := json.Marshal(apps)
	found := collected{names: map[string]*Candidate{}, paths: map[string]*Candidate{}}
	for _, a := range apps {
		addEvidence(found.names, a.ID+".exe", "", Evidence{ID: a.ID, App: a.Name})
	}
	// One name shared by a game and its demo; another shared by a game and software.
	addEvidence(found.names, "pair.exe", "", Evidence{ID: "dlc", App: "Game with DLC"})
	addEvidence(found.names, "pair.exe", "", Evidence{ID: "demo", App: "Game Demo"})
	addEvidence(found.names, "mixed.exe", "", Evidence{ID: "game", App: "Game"})
	addEvidence(found.names, "mixed.exe", "", Evidence{ID: "software", App: "Software"})
	r := SteamResponse{}
	r.Response.Items = []SteamItem{{730, 730, 1, &zero, &zero, "CS2"}, {431960, 431960, 1, &zero, &six, "Wallpaper Engine"}, {857320, 857320, 1, &zero, &six, "SRPG Studio"}, {ID: 42, Success: 2},
		{100, 100, 1, &zero, &zero, "Base"}, {101, 101, 1, &zero, &four, "Base DLC"}, {102, 102, 1, &zero, &one, "Base Demo"}}
	dir := t.TempDir()
	steam, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(dir, "steam.json"), steam, 0600)
	os.WriteFile(filepath.Join(dir, "xbox.json"), []byte(`{"Products":[{"ProductId":"9NT3WC6RKBVQ","ProductKind":"Game","LocalizedProperties":[{"ProductTitle":"G"}]},{"ProductId":"9WZDNCRFJ3TJ","ProductKind":"Application","LocalizedProperties":[{"ProductTitle":"A"}]}]}`), 0600)
	var parsed []App
	json.Unmarshal(data, &parsed)
	res, err := classify(parsed, found, sourceOptions{steamFile: filepath.Join(dir, "steam.json"), xboxFile: filepath.Join(dir, "xbox.json")})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range res.names {
		got[c.Name] = true
	}
	want := []string{"game.exe", "dlc.exe", "demo.exe", "xgame.exe", "pair.exe"}
	if len(got) != len(want) {
		t.Fatalf("unexpected games: %v", got)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing %s in %v", w, got)
		}
	}
	if len(res.unknown) != 3 || res.counts["non_game_or_conflicting_type"] != 5 || res.counts["shared_by_game_apps"] != 1 {
		t.Fatal(res.counts, len(res.unknown))
	}
}
