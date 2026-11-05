// Catalog data is untrusted input. Never execute downloaded content.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const discordURL = "https://discord.com/api/v10/applications/detectable"
const roscomURL = "https://raw.githubusercontent.com/roscomvpn/custom-category/release/mihomo/games.yaml"
const maxBytes = 64 << 20

type Executable struct {
	Name     string `json:"name"`
	OS       string `json:"os"`
	Launcher *bool  `json:"is_launcher"`
}
type App struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Executables []Executable `json:"executables"`
	SKUs        []SKU        `json:"third_party_skus"`
}
type Evidence struct {
	ID   string `json:"app_id"`
	App  string `json:"app_name"`
	Path string `json:"source_path"`
}
type Candidate struct {
	Name     string      `json:"process"`
	Evidence []Evidence  `json:"evidence"`
	Games    []GameProof `json:"game_proof,omitempty"`
}
type Report struct {
	Sources        map[string]string `json:"sources_sha256"`
	Excluded       map[string]int    `json:"excluded"`
	Total          int               `json:"total_candidates"`
	Candidates     []Candidate       `json:"candidates"`
	Unverified     []Candidate       `json:"unverified_not_for_publication"`
	Classification map[string]int    `json:"classification"`
}
type Rules struct {
	Exact map[string]bool
	Regex []*regexp.Regexp
}

// Deliberately limited YAML reader: only the plain scalar format verified upstream.
// A format change fails closed instead of silently dropping deduplication rules.
func parseRules(b []byte, regexAllowed bool) (Rules, error) {
	r := Rules{Exact: map[string]bool{}}
	header := false
	for i, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if !header {
			if s == "payload: []" {
				header = true
				continue
			}
			if s != "payload:" {
				return r, fmt.Errorf("line %d: expected payload", i+1)
			}
			header = true
			continue
		}
		if !strings.HasPrefix(s, "- ") {
			return r, fmt.Errorf("line %d: unsupported YAML", i+1)
		}
		s = strings.TrimSpace(strings.SplitN(strings.TrimPrefix(s, "- "), " #", 2)[0])
		parts := strings.SplitN(s, ",", 2)
		if len(parts) != 2 || parts[1] == "" {
			return r, fmt.Errorf("line %d: invalid rule", i+1)
		}
		switch parts[0] {
		case "PROCESS-NAME":
			if strings.ContainsAny(parts[1], ",\"'\r\n") {
				return r, fmt.Errorf("invalid process rule")
			}
			r.Exact[strings.ToLower(parts[1])] = true
		case "PROCESS-NAME-REGEX":
			if !regexAllowed {
				return r, fmt.Errorf("regex not allowed in local rules")
			}
			re, err := regexp.Compile(parts[1])
			if err != nil {
				return r, err
			}
			r.Regex = append(r.Regex, re)
		default:
			return r, fmt.Errorf("unsupported upstream rule: %s", parts[0])
		}
	}
	if !header {
		return r, fmt.Errorf("missing payload")
	}
	return r, nil
}
func (r Rules) covers(s string) bool {
	if r.Exact[strings.ToLower(s)] {
		return true
	}
	for _, re := range r.Regex {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _().!+\-]{1,119}\.exe$`)
var blocked = regexp.MustCompile(`(?i)(launcher|updat|unins|install|setup|crash|reporter|helper|service|anticheat|anti-cheat|battleye|webview|browser|bootstrap)`)
var sharedRuntime = regexp.MustCompile(`(?i)^(love|nw|nwjs|renpy|pythonw[0-9.]*|dotnet|rubyw?|perl|runner|runtime|engine|project|program|starter|start_game|application([-_].*)?|rpg_rt)\.exe$|(?i)(dotnetfx|vcredist|vc_redist|redistributable|prereq)`)
var generic = regexp.MustCompile(`(?i)^(game|mygame|client|server|main|app|application|start|launch|run|test|demo|shipping|win32|win64|x64|x86|ue4game|ue5game|unity|java|javaw|python[0-9.]*|node|electron|cmd|powershell|pwsh|wscript|cscript|rundll32|regsvr32|svchost|explorer|taskhostw|dllhost|conhost|msiexec|winlogon|lsass|services|smss|csrss|system|chrome|firefox|msedge|opera|discord|obs64|obs32|vlc|steam|origin|upc|notepad|calc)([-_ ]?(32|64|x64|x86))?\.exe$`)

func normalize(s string) (string, bool) {
	if strings.ContainsAny(s, "\r\n\x00,*?<>|\"$") {
		return "", false
	}
	s = path.Base(strings.ReplaceAll(s, "\\", "/"))
	s = strings.ToLower(s)
	return s, safeName.MatchString(s) && !blocked.MatchString(s) && !generic.MatchString(s) && !sharedRuntime.MatchString(s)
}
func collect(data []byte, base, current Rules, rejected map[string]bool) ([]Candidate, map[string]int, error) {
	var apps []App
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, nil, err
	}
	if len(apps) == 0 {
		return nil, nil, fmt.Errorf("empty Discord catalog")
	}
	counts := map[string]int{}
	all := map[string]*Candidate{}
	seen := map[string]bool{}
	windows := 0
	for _, app := range apps {
		if app.ID == "" || app.Name == "" {
			return nil, nil, fmt.Errorf("missing application identity")
		}
		for _, exe := range app.Executables {
			if exe.OS == "" || exe.Name == "" {
				return nil, nil, fmt.Errorf("invalid executable schema")
			}
			if exe.OS != "win32" {
				counts["non_windows"]++
				continue
			}
			windows++
			if exe.Launcher == nil {
				return nil, nil, fmt.Errorf("missing is_launcher")
			}
			if *exe.Launcher {
				counts["launcher"]++
				continue
			}
			name, ok := normalize(exe.Name)
			if !ok {
				counts["unsafe_or_generic"]++
				continue
			}
			if base.covers(name) || base.covers(path.Base(strings.ReplaceAll(exe.Name, "\\", "/"))) {
				counts["roscomvpn"]++
				continue
			}
			if current.covers(name) {
				counts["already_local"]++
				continue
			}
			if rejected[name] {
				counts["manually_rejected"]++
				continue
			}
			key := name + "\x00" + app.ID + "\x00" + exe.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			if all[name] == nil {
				all[name] = &Candidate{Name: name, Evidence: []Evidence{}}
			}
			all[name].Evidence = append(all[name].Evidence, Evidence{app.ID, app.Name, exe.Name})
		}
	}
	if windows == 0 {
		return nil, nil, fmt.Errorf("no Windows executables; schema may have changed")
	}
	out := []Candidate{}
	for _, c := range all {
		ids := map[string]bool{}
		for _, e := range c.Evidence {
			ids[e.ID] = true
		}
		if len(ids) > 1 {
			counts["ambiguous_multiple_apps"]++
			continue
		}
		sort.Slice(c.Evidence, func(i, j int) bool { return c.Evidence[i].Path < c.Evidence[j].Path })
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, counts, nil
}
func readSource(file, url string) ([]byte, error) {
	if file != "" {
		b, e := os.ReadFile(file)
		if len(b) > maxBytes {
			return nil, fmt.Errorf("source too large")
		}
		return b, e
	}
	client := http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return fmt.Errorf("redirect refused") }}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nix-games-catalog/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("source HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if len(b) > maxBytes {
		return nil, fmt.Errorf("source too large")
	}
	return b, err
}
func run() error {
	discord := flag.String("discord-file", "", "offline Discord snapshot")
	roscom := flag.String("roscom-file", "", "offline RoscomVPN snapshot")
	approve := flag.String("approve", "", "comma-separated manually verified candidates; writes local YAML only")
	approveGames := flag.Bool("approve-games", false, "explicitly stage ALL classified games in local YAML for a manual publication PR")
	steamFile := flag.String("steam-file", "", "offline Steam classification snapshot")
	saveSteam := flag.String("save-steam", "", "save Steam classification snapshot for reproducible review")
	flag.Parse()
	if *approveGames && *approve != "" {
		return fmt.Errorf("choose -approve or -approve-games")
	}
	d, err := readSource(*discord, discordURL)
	if err != nil {
		return err
	}
	b, err := readSource(*roscom, roscomURL)
	if err != nil {
		return err
	}
	base, err := parseRules(b, true)
	if err != nil {
		return err
	}
	if len(base.Exact)+len(base.Regex) == 0 {
		return fmt.Errorf("empty RoscomVPN baseline")
	}
	local, err := os.ReadFile("nix-games.yaml")
	if err != nil {
		return err
	}
	current, err := parseRules(local, false)
	if err != nil {
		return err
	}
	rejectBytes, err := os.ReadFile("review/rejected.json")
	if err != nil {
		return err
	}
	var rejects []string
	if err = json.Unmarshal(rejectBytes, &rejects); err != nil {
		return err
	}
	rejected := map[string]bool{}
	for _, s := range rejects {
		rejected[strings.ToLower(s)] = true
	}
	candidates, counts, err := collect(d, base, current, rejected)
	if err != nil {
		return err
	}
	candidates, unknown, classified, steamHash, err := classify(d, candidates, *steamFile, *saveSteam)
	if err != nil {
		return err
	}
	if *approveGames {
		if len(candidates) == 0 {
			return fmt.Errorf("no classified games to approve")
		}
		names := []string{}
		for _, c := range candidates {
			names = append(names, c.Name)
		}
		*approve = strings.Join(names, ",")
	}
	if *approve != "" {
		allowed := map[string]bool{}
		for _, c := range candidates {
			allowed[c.Name] = true
		}
		for _, s := range strings.Split(*approve, ",") {
			s = strings.ToLower(strings.TrimSpace(s))
			if !allowed[s] {
				return fmt.Errorf("not a current eligible candidate: %s", s)
			}
			current.Exact[s] = true
		}
		names := []string{}
		for s := range current.Exact {
			names = append(names, s)
		}
		sort.Strings(names)
		out := "payload:\n"
		for _, s := range names {
			out += "  - PROCESS-NAME," + s + "\n"
		}
		return os.WriteFile("nix-games.yaml", []byte(out), 0644)
	}
	total := len(candidates)
	report := Report{Sources: map[string]string{discordURL: fmt.Sprintf("%x", sha256.Sum256(d)), roscomURL: fmt.Sprintf("%x", sha256.Sum256(b)), steamURL: steamHash}, Excluded: counts, Total: total, Candidates: candidates, Unverified: unknown, Classification: classified}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile("review/candidates.json", append(out, '\n'), 0644); err != nil {
		return err
	}
	fmt.Printf("Eligible candidates: %d; review batch: %d. Live YAML unchanged.\n", total, len(candidates))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
