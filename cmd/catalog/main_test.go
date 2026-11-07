package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

func TestNames(t *testing.T) {
	for _, s := range []string{"love.exe", "runner.exe", "project.exe", "start_game.exe", "dotnetfx40_full_x86_x64.exe", "vc_redist.x64.exe", "application-steam-x64.exe", "pythonw.exe"} {
		if _, ok := normalize(s); ok {
			t.Errorf("accepted shared runtime/installer %q", s)
		}
	}
	for _, s := range []string{"game.exe", "launcher.exe", "javaw.exe", "svchost.exe", "cmd.exe", "game64.exe", "Discord.exe", "steam.exe", "EasyAntiCheat.exe", "abc,evil.exe", "bad\nname.exe", "*.exe", "bad$.exe", "a.exe", "test.exe", "a #b.exe", "x: y.exe", `q"t.exe`, "crashsender1301.exe", "gh.exe", "sh.exe", "split.exe", "'lead.exe", "&anchor.exe"} {
		if _, ok := normalize(s); ok {
			t.Errorf("accepted unsafe %q", s)
		}
	}
	for _, s := range []string{"folder/SpecificGame.exe", `folder\SpecificGame.exe`, "Specific Game.exe", "warhammer 40,000 rogue trader/wh40krt.exe", "them's fightin' herds.exe", "fire & maneuver.exe", "crashlands.exe", "crash bandicoot 4.exe", "アトリエ.exe"} {
		if _, ok := normalize(s); !ok {
			t.Errorf("rejected %q", s)
		}
	}
}
func TestPathRule(t *testing.T) {
	suffix, rule, ok := pathRule(`Mark_Of_The_Ninja\bin\game.exe`)
	if !ok || suffix != "mark_of_the_ninja/bin/game.exe" {
		t.Fatal(suffix, rule, ok)
	}
	re := regexp.MustCompile(rule)
	for _, s := range []string{`C:\Steam\steamapps\common\mark_of_the_ninja\bin\game.exe`, `D:\Games\Mark_of_the_Ninja\BIN\Game.exe`} {
		if !re.MatchString(s) {
			t.Errorf("rule %s missed %s", rule, s)
		}
	}
	for _, s := range []string{`C:\x\not_mark_of_the_ninja\bin\game.exe`, `C:\mark_of_the_ninja\bin\game.exe.bak`, `C:\other\game.exe`} {
		if re.MatchString(s) {
			t.Errorf("rule %s matched %s", rule, s)
		}
	}
	_, rule, ok = pathRule("shattered lights (v1.2)/project.exe")
	if !ok || !regexp.MustCompile(rule).MatchString(`C:\g\Shattered Lights (v1.2)\project.exe`) {
		t.Fatal("metacharacters not quoted", rule)
	}
	for _, s := range []string{"game.exe", "bin/game.exe", "win64/bin/client.exe", "tools/java.exe", "my game/cmd.exe", "my game/launcher.exe", "../game.exe", "a,b/game.exe", "a #b/game.exe"} {
		if _, _, ok := pathRule(s); ok {
			t.Errorf("accepted path %q", s)
		}
	}
}
func TestRules(t *testing.T) {
	r, err := parseRules([]byte("payload:\n  - PROCESS-NAME,CS2.exe # comment\n  - PROCESS-NAME-REGEX,.*Win64-Shipping.*\n"), "PROCESS-NAME", "PROCESS-NAME-REGEX")
	if err != nil {
		t.Fatal(err)
	}
	// Mihomo regexes are case-insensitive even without (?i).
	for _, s := range []string{"cs2.exe", "Something-Win64-Shipping.exe", "x-win64-shipping.exe"} {
		if !r.covers(s) {
			t.Error(s)
		}
	}
	for _, s := range []string{"", "payload:\n - DOMAIN,x.com", "payload:\n - PROCESS-NAME-REGEX,[", "payload:\n - 'PROCESS-NAME,x.exe'", "payload:\n - PROCESS-PATH-REGEX,x"} {
		if _, err := parseRules([]byte(s), "PROCESS-NAME", "PROCESS-NAME-REGEX"); err == nil {
			t.Errorf("accepted malformed %q", s)
		}
	}
	if _, err := parseRules([]byte("payload:\n - PROCESS-NAME-REGEX,x"), "PROCESS-NAME"); err == nil {
		t.Error("regex accepted in local names file")
	}
}

var no, yes = false, true

func init() {
	if err := loadSystemNames("../../review/system-binaries.txt"); err != nil {
		panic(err)
	}
}

func TestCollect(t *testing.T) {
	apps := []App{
		{ID: "1", Name: "Game One", Executables: []Executable{{"Dir/Unique.exe", "win32", &no}, {"Dir/Unique.exe", "win32", &no}, {"cs2.exe", "win32", &no}, {"Other-Win64-Shipping.exe", "win32", &no}, {"app.exe", "darwin", &no}, {"CustomLaunch.exe", "win32", &yes}, {"shared.exe", "win32", &no}, {"known.exe", "win32", &no}, {"reject.exe", "win32", &no}, {"my game/bin/game.exe", "win32", &no}, {"game.exe", "win32", &no}}},
		{ID: "2", Name: "Game Two", Executables: []Executable{{"shared.exe", "win32", &no}}},
	}
	data, _ := json.Marshal(apps)
	base, _ := parseRules([]byte("payload:\n - PROCESS-NAME,cs2.exe\n - PROCESS-NAME-REGEX,(?i).*Win64-Shipping.*"), "PROCESS-NAME", "PROCESS-NAME-REGEX")
	current := Rules{Exact: map[string]bool{"known.exe": true}, Paths: map[string]bool{}}
	got, _, counts, err := collect(data, base, current, map[string]bool{"reject.exe": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.names) != 2 || len(got.names["unique.exe"].Evidence) != 1 || len(got.names["shared.exe"].Evidence) != 2 {
		t.Fatalf("unexpected candidates: %+v", got.names)
	}
	if len(got.paths) != 1 || got.paths["my game/bin/game.exe"] == nil {
		t.Fatalf("unexpected path candidates: %+v", got.paths)
	}
	if counts["roscomvpn"] != 2 || counts["unsafe_or_generic"] != 1 || counts["already_local"] != 1 || counts["manually_rejected"] != 1 {
		t.Fatal(counts)
	}
}
func TestFailClosed(t *testing.T) {
	for _, s := range []string{`[]`, `{}`, `[{"id":"1","name":"X","executables":[{"os":"windows","name":"valid.exe","is_launcher":false}]}]`, `[{"id":"1","name":"X","executables":[{"os":"win32","name":"valid.exe"}]}]`} {
		if _, _, _, err := collect([]byte(s), Rules{}, Rules{}, nil); err == nil {
			t.Errorf("accepted schema drift: %s", s)
		}
	}
}
func TestRepositoryFiles(t *testing.T) {
	b, err := os.ReadFile("../../" + namesFile)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseRules(b, "PROCESS-NAME")
	if err != nil {
		t.Fatal(err)
	}
	for s := range r.Exact {
		if _, ok := normalize(s); !ok {
			t.Errorf("unsafe live process: %s", s)
		}
	}
	if b, err = os.ReadFile("../../" + pathsFile); err == nil {
		p, err := parseRules(b, "PROCESS-PATH-REGEX")
		if err != nil {
			t.Fatal(err)
		}
		shape := regexp.MustCompile(`^\(\?i\)\(\^\|\[\\\\/\]\)[^,#"]+\$$`)
		for s := range p.Paths {
			if !shape.MatchString(s) {
				t.Errorf("unexpected live path rule: %s", s)
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
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
