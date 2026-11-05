package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestNames(t *testing.T) {
	for _, s := range []string{"love.exe", "runner.exe", "project.exe", "start_game.exe", "dotnetfx40_full_x86_x64.exe", "vc_redist.x64.exe", "application-steam-x64.exe", "pythonw.exe"} {
		if _, ok := normalize(s); ok {
			t.Errorf("accepted shared runtime/installer %q", s)
		}
	}
	for _, s := range []string{"game.exe", "launcher.exe", "javaw.exe", "svchost.exe", "cmd.exe", "game64.exe", "Discord.exe", "steam.exe", "EasyAntiCheat.exe", "abc,evil.exe", "bad\nname.exe", "*.exe", "bad$.exe", "a.exe", "test.exe"} {
		if _, ok := normalize(s); ok {
			t.Errorf("accepted unsafe %q", s)
		}
	}
	for _, s := range []string{"folder/SpecificGame.exe", `folder\SpecificGame.exe`, "Specific Game.exe"} {
		if _, ok := normalize(s); !ok {
			t.Errorf("rejected %q", s)
		}
	}
}
func TestRules(t *testing.T) {
	r, err := parseRules([]byte("payload:\n  - PROCESS-NAME,CS2.exe # comment\n  - PROCESS-NAME-REGEX,(?i).*Win64-Shipping.*\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"cs2.exe", "Something-Win64-Shipping.exe"} {
		if !r.covers(s) {
			t.Error(s)
		}
	}
	for _, s := range []string{"", "payload:\n - DOMAIN,x.com", "payload:\n - PROCESS-NAME-REGEX,[", "payload:\n - 'PROCESS-NAME,x.exe'"} {
		if _, err := parseRules([]byte(s), true); err == nil {
			t.Errorf("accepted malformed %q", s)
		}
	}
}
func TestCollect(t *testing.T) {
	no, yes := false, true
	apps := []App{
		{ID: "1", Name: "Game One", Executables: []Executable{{"Dir/Unique.exe", "win32", &no}, {"Dir/Unique.exe", "win32", &no}, {"cs2.exe", "win32", &no}, {"Other-Win64-Shipping.exe", "win32", &no}, {"app.exe", "darwin", &no}, {"CustomLaunch.exe", "win32", &yes}, {"shared.exe", "win32", &no}, {"known.exe", "win32", &no}, {"reject.exe", "win32", &no}}},
		{ID: "2", Name: "Game Two", Executables: []Executable{{"shared.exe", "win32", &no}}},
	}
	data, _ := json.Marshal(apps)
	base, _ := parseRules([]byte("payload:\n - PROCESS-NAME,cs2.exe\n - PROCESS-NAME-REGEX,(?i).*Win64-Shipping.*"), true)
	current := Rules{Exact: map[string]bool{"known.exe": true}}
	got, counts, err := collect(data, base, current, map[string]bool{"reject.exe": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "unique.exe" || len(got[0].Evidence) != 1 {
		t.Fatalf("unexpected candidates: %+v", got)
	}
	if counts["roscomvpn"] != 2 || counts["ambiguous_multiple_apps"] != 1 {
		t.Fatal(counts)
	}
}
func TestFailClosed(t *testing.T) {
	for _, s := range []string{`[]`, `{}`, `[{"id":"1","name":"X","executables":[{"os":"windows","name":"valid.exe","is_launcher":false}]}]`, `[{"id":"1","name":"X","executables":[{"os":"win32","name":"valid.exe"}]}]`} {
		if _, _, err := collect([]byte(s), Rules{}, Rules{}, nil); err == nil {
			t.Errorf("accepted schema drift: %s", s)
		}
	}
}
func TestRepositoryFiles(t *testing.T) {
	b, err := os.ReadFile("../../nix-games.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseRules(b, false)
	if err != nil {
		t.Fatal(err)
	}
	for s := range r.Exact {
		if _, ok := normalize(s); !ok {
			t.Errorf("unsafe live process: %s", s)
		}
	}
	b, err = os.ReadFile("../../review/rejected.json")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err = json.Unmarshal(b, &names); err != nil {
		t.Fatal(err)
	}
}
