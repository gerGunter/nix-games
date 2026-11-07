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

const namesFile = "nix-games.yaml"
const pathsFile = "nix-games-paths.yaml"

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

// Candidate is either a PROCESS-NAME (Name is the lowercase file name) or a
// PROCESS-PATH-REGEX (Name is the lowercase Discord path suffix, Regex the rule).
type Candidate struct {
	Name     string      `json:"process"`
	Regex    string      `json:"path_regex,omitempty"`
	Evidence []Evidence  `json:"evidence"`
	Games    []GameProof `json:"game_proof,omitempty"`
}
type Report struct {
	Sources        map[string]string `json:"sources_sha256"`
	Excluded       map[string]int    `json:"excluded"`
	Total          int               `json:"total_candidates"`
	Candidates     []Candidate       `json:"candidates"`
	PathCandidates []Candidate       `json:"path_candidates"`
	Unverified     []Candidate       `json:"unverified_not_for_publication"`
	Classification map[string]int    `json:"classification"`
}
type Rules struct {
	Exact map[string]bool
	Regex []*regexp.Regexp
	Paths map[string]bool // PROCESS-PATH-REGEX payloads, verbatim
}

// Deliberately limited YAML reader: only the plain scalar format verified upstream.
// A format change fails closed instead of silently dropping deduplication rules.
// allowed lists the rule types accepted in this file.
func parseRules(b []byte, allowed ...string) (Rules, error) {
	r := Rules{Exact: map[string]bool{}, Paths: map[string]bool{}}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
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
		if !ok[parts[0]] {
			return r, fmt.Errorf("line %d: unsupported rule here: %s", i+1, parts[0])
		}
		switch parts[0] {
		case "PROCESS-NAME":
			if strings.ContainsAny(parts[1], ",\"\r\n") {
				return r, fmt.Errorf("line %d: invalid process rule", i+1)
			}
			r.Exact[strings.ToLower(parts[1])] = true
		case "PROCESS-NAME-REGEX":
			// Mihomo compiles process regexes case-insensitively; match that.
			re, err := regexp.Compile("(?i)" + parts[1])
			if err != nil {
				return r, err
			}
			r.Regex = append(r.Regex, re)
		case "PROCESS-PATH-REGEX":
			if _, err := regexp.Compile(parts[1]); err != nil {
				return r, err
			}
			r.Paths[parts[1]] = true
		default:
			return r, fmt.Errorf("line %d: unsupported rule: %s", i+1, parts[0])
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

// Letters/digits of any script plus punctuation that is safe inside a Mihomo
// classical rule and a YAML plain scalar. Excluded: , (rule separator), # (comment),
// : (YAML mapping), quotes, shell/glob metacharacters.
var safeName = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} _().!+\-'&@~\[\]]{1,119}\.exe$`)
var safeSegment = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} _().!+\-'&@~\[\]]{0,119}$`)
var blocked = regexp.MustCompile(`(?i)(launcher|updat|unins|install|setup|crashreport|crashhandler|crashpad|crashsender|crash_reporter|reporter|helper|service|anticheat|anti-cheat|battleye|webview|browser|bootstrap)`)
var sharedRuntime = regexp.MustCompile(`(?i)^(love|nw|nwjs|renpy|pythonw[0-9.]*|dotnet|rubyw?|perl|runner|runtime|engine|project|program|starter|start_game|application([-_].*)?|rpg_rt)\.exe$|(?i)(dotnetfx|vcredist|vc_redist|redistributable|prereq)`)
var generic = regexp.MustCompile(`(?i)^(game|mygame|client|server|main|app|application|start|launch|run|test|demo|shipping|win32|win64|x64|x86|ue4game|ue5game|unity|java|javaw|python[0-9.]*|node|electron|cmd|powershell|pwsh|wscript|cscript|rundll32|regsvr32|svchost|explorer|taskhostw|dllhost|conhost|msiexec|winlogon|lsass|services|smss|csrss|system|chrome|firefox|msedge|opera|discord|obs64|obs32|vlc|steam|origin|upc|notepad|calc` +
	// Tools and wrappers shipped with many unrelated games, and engine/emulator hosts.
	`|autorun|4gb_patch|large_address_aware|x360ce|xinput_plus|config|configtool|configuration|configurationtool|settings|setting|options|modmanager|mod_manager|modloader|pbsvc|pnkbstr[ab]?|dosbox(-x)?|scummvm|build|default|play|player|launchpad|splash|steamrun|testapp|udk|gameclient|clientg|bin|data|[\p{L}\p{N}]{1,2})([-_ ]?(32|64|x64|x86))?\.exe$`)

// Directory names that say nothing about which game a binary belongs to.
var genericDir = regexp.MustCompile(`(?i)^(bin|bin32|bin64|binaries|win32|win64|windows|windowsnoeditor|x64|x86|amd64|game|games|client|app|data|release|retail|build|builds|shipping|launcher|common|program files( \(x86\))?|steamapps|.{0,2})$`)

// Never pass a system/shell binary to a path rule, even with a distinctive folder.
var systemBinary = regexp.MustCompile(`(?i)^(java|javaw|python[0-9.]*|pythonw[0-9.]*|node|electron|cmd|powershell|pwsh|wscript|cscript|rundll32|regsvr32|svchost|explorer|taskhostw|dllhost|conhost|msiexec|winlogon|lsass|services|smss|csrss|system|chrome|firefox|msedge|opera|discord|obs64|obs32|vlc|steam|origin|upc|notepad|calc|dotnet|perl|rubyw?)\.exe$|(dotnetfx|vcredist|vc_redist|redistributable|prereq)`)

// systemNames is a denylist of binaries that ship with Windows, Git for Windows
// and common developer tools (review/system-binaries.txt). A game catalog entry
// with such a name would route unrelated programs (sh.exe, gh.exe, sc.exe…).
var systemNames = map[string]bool{}

func loadSystemNames(file string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if s := strings.ToLower(strings.TrimSpace(line)); s != "" && !strings.HasPrefix(s, "#") {
			systemNames[s] = true
		}
	}
	if len(systemNames) == 0 {
		return fmt.Errorf("empty %s", file)
	}
	return nil
}

func baseName(raw string) string {
	return strings.ToLower(path.Base(strings.ReplaceAll(raw, "\\", "/")))
}

// normalize validates only the file name: a comma or other odd character in a
// parent directory of the Discord path is irrelevant for PROCESS-NAME.
func normalize(raw string) (string, bool) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", false
	}
	s := baseName(raw)
	return s, safeName.MatchString(s) && !blocked.MatchString(s) && !generic.MatchString(s) && !sharedRuntime.MatchString(s) && !systemNames[s]
}

// pathRule builds a PROCESS-PATH-REGEX for a generic binary (game.exe, nw.exe…)
// whose Discord path carries a distinctive install folder. Discord paths are a
// suffix of the real path, so the rule anchors on a separator before the suffix.
func pathRule(raw string) (suffix, rule string, ok bool) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", "", false
	}
	segs := strings.Split(strings.Trim(strings.ToLower(strings.ReplaceAll(raw, "\\", "/")), "/"), "/")
	if len(segs) < 2 {
		return "", "", false
	}
	file := segs[len(segs)-1]
	if !safeName.MatchString(file) || systemBinary.MatchString(file) || systemNames[file] || blocked.MatchString(file) {
		return "", "", false
	}
	distinctive := false
	quoted := []string{}
	for _, seg := range segs[:len(segs)-1] {
		if seg == "." || seg == ".." || !safeSegment.MatchString(seg) {
			return "", "", false
		}
		if !genericDir.MatchString(seg) {
			distinctive = true
		}
		quoted = append(quoted, regexp.QuoteMeta(seg))
	}
	if !distinctive {
		return "", "", false
	}
	quoted = append(quoted, regexp.QuoteMeta(file))
	rule = `(?i)(^|[\\/])` + strings.Join(quoted, `[\\/]`) + `$`
	return strings.Join(segs, "/"), rule, true
}

type collected struct {
	names map[string]*Candidate
	paths map[string]*Candidate
}

func addEvidence(m map[string]*Candidate, key, regex string, e Evidence) {
	if m[key] == nil {
		m[key] = &Candidate{Name: key, Regex: regex, Evidence: []Evidence{}}
	}
	for _, old := range m[key].Evidence {
		if old == e {
			return
		}
	}
	m[key].Evidence = append(m[key].Evidence, e)
}

// collect gathers every eligible Windows executable together with ALL Discord
// applications that list it. Whether a name shared by several applications is
// safe is decided later, once each application is classified.
func collect(data []byte, base, current Rules, rejected map[string]bool) (collected, []App, map[string]int, error) {
	var apps []App
	out := collected{map[string]*Candidate{}, map[string]*Candidate{}}
	if err := json.Unmarshal(data, &apps); err != nil {
		return out, nil, nil, err
	}
	if len(apps) == 0 {
		return out, nil, nil, fmt.Errorf("empty Discord catalog")
	}
	counts := map[string]int{}
	windows := 0
	for _, app := range apps {
		if app.ID == "" || app.Name == "" {
			return out, nil, nil, fmt.Errorf("missing application identity")
		}
		for _, exe := range app.Executables {
			if exe.OS == "" || exe.Name == "" {
				return out, nil, nil, fmt.Errorf("invalid executable schema")
			}
			if exe.OS != "win32" {
				counts["non_windows"]++
				continue
			}
			windows++
			if exe.Launcher == nil {
				return out, nil, nil, fmt.Errorf("missing is_launcher")
			}
			if *exe.Launcher {
				counts["launcher"]++
				continue
			}
			ev := Evidence{app.ID, app.Name, exe.Name}
			raw := path.Base(strings.ReplaceAll(exe.Name, "\\", "/"))
			if base.covers(raw) || base.covers(baseName(exe.Name)) {
				counts["roscomvpn"]++
				continue
			}
			name, ok := normalize(exe.Name)
			if ok {
				switch {
				case current.covers(name):
					counts["already_local"]++
				case rejected[name]:
					counts["manually_rejected"]++
				default:
					addEvidence(out.names, name, "", ev)
				}
				continue
			}
			suffix, rule, pok := "", "", false
			if safeName.MatchString(name) && !blocked.MatchString(name) {
				suffix, rule, pok = pathRule(exe.Name)
			}
			if !pok {
				counts["unsafe_or_generic"]++
				continue
			}
			switch {
			case current.Paths[rule]:
				counts["already_local_path"]++
			case rejected[suffix]:
				counts["manually_rejected"]++
			default:
				addEvidence(out.paths, suffix, rule, ev)
			}
		}
	}
	if windows == 0 {
		return out, nil, nil, fmt.Errorf("no Windows executables; schema may have changed")
	}
	return out, apps, counts, nil
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
	var last error
	// Transient failures (rate limiting, 5xx) are retried; anything else fails closed.
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*attempt) * 2 * time.Second)
		}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "nix-games-catalog/1.0")
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			last = fmt.Errorf("source HTTP %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("source HTTP %d", resp.StatusCode)
		}
		if len(b) > maxBytes {
			return nil, fmt.Errorf("source too large")
		}
		return b, err
	}
	return nil, last
}
func writeRules(file, kind string, rules map[string]bool) error {
	names := []string{}
	for s := range rules {
		names = append(names, s)
	}
	sort.Strings(names)
	var out strings.Builder
	out.WriteString("payload:\n")
	for _, s := range names {
		out.WriteString("  - " + kind + "," + s + "\n")
	}
	return os.WriteFile(file, []byte(out.String()), 0644)
}
func readLocal(file string, kind string) (Rules, error) {
	b, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return parseRules([]byte("payload: []\n"), kind)
	}
	if err != nil {
		return Rules{}, err
	}
	return parseRules(b, kind)
}
func run() error {
	discord := flag.String("discord-file", "", "offline Discord snapshot")
	roscom := flag.String("roscom-file", "", "offline RoscomVPN snapshot")
	approve := flag.String("approve", "", "comma-separated manually verified candidates (process names or path suffixes); writes local YAML only")
	approveGames := flag.Bool("approve-games", false, "explicitly stage ALL classified games in local YAML for a manual publication PR")
	steamFile := flag.String("steam-file", "", "offline Steam classification snapshot")
	saveSteam := flag.String("save-steam", "", "save Steam classification snapshot for reproducible review")
	xboxFile := flag.String("xbox-file", "", "offline Microsoft Store classification snapshot")
	saveXbox := flag.String("save-xbox", "", "save Microsoft Store classification snapshot for reproducible review")
	flag.Parse()
	if *approveGames && *approve != "" {
		return fmt.Errorf("choose -approve or -approve-games")
	}
	if err := loadSystemNames("review/system-binaries.txt"); err != nil {
		return err
	}
	d, err := readSource(*discord, discordURL)
	if err != nil {
		return err
	}
	b, err := readSource(*roscom, roscomURL)
	if err != nil {
		return err
	}
	base, err := parseRules(b, "PROCESS-NAME", "PROCESS-NAME-REGEX")
	if err != nil {
		return err
	}
	if len(base.Exact)+len(base.Regex) == 0 {
		return fmt.Errorf("empty RoscomVPN baseline")
	}
	current, err := readLocal(namesFile, "PROCESS-NAME")
	if err != nil {
		return err
	}
	currentPaths, err := readLocal(pathsFile, "PROCESS-PATH-REGEX")
	if err != nil {
		return err
	}
	current.Paths = currentPaths.Paths
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
	found, apps, counts, err := collect(d, base, current, rejected)
	if err != nil {
		return err
	}
	res, err := classify(apps, found, sourceOptions{*steamFile, *saveSteam, *xboxFile, *saveXbox})
	if err != nil {
		return err
	}
	if *approveGames {
		if len(res.names)+len(res.paths) == 0 {
			return fmt.Errorf("no classified games to approve")
		}
		list := []string{}
		for _, c := range res.names {
			list = append(list, c.Name)
		}
		for _, c := range res.paths {
			list = append(list, c.Name)
		}
		*approve = strings.Join(list, ",")
	}
	if *approve != "" {
		names := map[string]bool{}
		paths := map[string]string{}
		for _, c := range res.names {
			names[c.Name] = true
		}
		for _, c := range res.paths {
			paths[c.Name] = c.Regex
		}
		for _, s := range strings.Split(*approve, ",") {
			s = strings.ToLower(strings.TrimSpace(s))
			switch {
			case names[s]:
				current.Exact[s] = true
			case paths[s] != "":
				current.Paths[paths[s]] = true
			default:
				return fmt.Errorf("not a current eligible candidate: %s", s)
			}
		}
		// Entries that no longer pass the safety filters are pruned, never kept silently.
		for s := range current.Exact {
			if _, ok := normalize(s); !ok {
				fmt.Fprintf(os.Stderr, "Pruned unsafe live name: %s\n", s)
				delete(current.Exact, s)
			}
		}
		if err := writeRules(namesFile, "PROCESS-NAME", current.Exact); err != nil {
			return err
		}
		return writeRules(pathsFile, "PROCESS-PATH-REGEX", current.Paths)
	}
	report := Report{
		Sources:        map[string]string{discordURL: fmt.Sprintf("%x", sha256.Sum256(d)), roscomURL: fmt.Sprintf("%x", sha256.Sum256(b)), steamURL: res.steamHash, xboxURL: res.xboxHash},
		Excluded:       counts,
		Total:          len(res.names) + len(res.paths),
		Candidates:     res.names,
		PathCandidates: res.paths,
		Unverified:     res.unknown,
		Classification: res.counts,
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile("review/candidates.json", append(out, '\n'), 0644); err != nil {
		return err
	}
	fmt.Printf("Eligible: %d process names, %d path rules; unverified: %d. Live YAML unchanged.\n", len(res.names), len(res.paths), len(res.unknown))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
