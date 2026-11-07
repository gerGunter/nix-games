package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParsePics(t *testing.T) {
	b := []byte(`{"status":"success","data":{"413150":{"common":{"type":"Game"},"config":{"installdir":"Stardew Valley","launch":{
		"0":{"config":{"oslist":"windows"},"executable":"Stardew Valley.exe"},
		"1":{"config":{"oslist":"macos"},"executable":"Contents/MacOS/StardewValley"},
		"2":{"executable":"bin\\Win64\\Other.exe"},
		"3":{"config":{"oslist":"windows"},"executable":"manual.pdf"},
		"4":{"config":{"oslist":"linux"},"executable":"linux.exe"}}}}}}`)
	a, err := parsePics(413150, b)
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != "game" || a.InstallDir != "Stardew Valley" || len(a.Launch) != 2 || a.Launch[0] != "Stardew Valley.exe" || a.Launch[1] != "bin/Win64/Other.exe" {
		t.Fatalf("%+v", a)
	}
	if a, err = parsePics(3, []byte(`{"status":"success","data":{"3":{}}}`)); err != nil || len(a.Launch) != 0 {
		t.Fatal("unknown app must be empty, not an error", a, err)
	}
	for _, s := range []string{`{}`, `{"status":"error","data":{"1":{}}}`, `{"status":"success","data":{"2":{}}}`} {
		if _, err := parsePics(1, []byte(s)); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
}

func TestPicsCandidates(t *testing.T) {
	zero := 0
	apps := []App{
		{ID: "a", Name: "Stardew", SKUs: []SKU{{"steam", "413150"}}},
		{ID: "b", Name: "Not in PICS", SKUs: []SKU{{"steam", "5"}}},
		// Has Discord paths, so PICS is not consulted.
		{ID: "c", Name: "Discord game", Executables: []Executable{{"discord.game.exe", "win32", &no}}, SKUs: []SKU{{"steam", "6"}}},
	}
	r := SteamResponse{}
	r.Response.Items = []SteamItem{{413150, 413150, 1, &zero, &zero, "Stardew Valley"}, {5, 5, 1, &zero, &zero, "Five"}, {6, 6, 1, &zero, &zero, "Six"}}
	dir := t.TempDir()
	steam, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(dir, "steam.json"), steam, 0600)
	pics, _ := json.Marshal([]PicsApp{
		{ID: 413150, Type: "game", InstallDir: "Stardew Valley", Launch: []string{"Stardew Valley.exe", "game.exe", "launcher/launcher.exe"}},
		{ID: 5, Launch: []string{}},
	})
	os.WriteFile(filepath.Join(dir, "pics.json"), pics, 0600)
	col := newCollector(Rules{Exact: map[string]bool{}}, Rules{Exact: map[string]bool{}, Paths: map[string]bool{}}, nil)
	res, err := classify(apps, col, sourceOptions{steamFile: filepath.Join(dir, "steam.json"), picsFile: filepath.Join(dir, "pics.json")})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.names) != 1 || res.names[0].Name != "stardew valley.exe" || res.names[0].Evidence[0].Source != "steam_pics:413150" {
		t.Fatalf("names: %+v", res.names)
	}
	if len(res.paths) != 1 || res.paths[0].Name != "stardew valley/game.exe" {
		t.Fatalf("paths: %+v", res.paths)
	}
	// A snapshot that lacks a requested app fails closed.
	os.WriteFile(filepath.Join(dir, "pics.json"), []byte(`[]`), 0600)
	col = newCollector(Rules{Exact: map[string]bool{}}, Rules{Exact: map[string]bool{}, Paths: map[string]bool{}}, nil)
	if _, err := classify(apps, col, sourceOptions{steamFile: filepath.Join(dir, "steam.json"), picsFile: filepath.Join(dir, "pics.json")}); err == nil {
		t.Fatal("accepted incomplete PICS snapshot")
	}
}

func TestDemoteShared(t *testing.T) {
	col := newCollector(Rules{Exact: map[string]bool{}}, Rules{Exact: map[string]bool{}, Paths: map[string]bool{}}, nil)
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		col.add("game "+id+"/tdf.exe", Evidence{ID: id, App: "G" + id, Path: "game " + id + "/tdf.exe"}, "")
	}
	col.add("one/tdf2.exe", Evidence{ID: "1", Path: "one/tdf2.exe"}, "")
	col.demoteShared()
	if col.out.names["tdf.exe"] != nil || col.out.names["tdf2.exe"] == nil || len(col.out.paths) != 5 {
		t.Fatalf("%v %v", col.out.names, col.out.paths)
	}
}
