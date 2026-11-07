package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const steamURL = "https://api.steampowered.com/IStoreBrowseService/GetItems/v1/"
const xboxURL = "https://displaycatalog.mp.microsoft.com/v7.0/products"

type SKU struct {
	Distributor string `json:"distributor"`
	ID          string `json:"id"`
}
type SteamItem struct {
	AppID    int    `json:"appid"`
	ID       int    `json:"id"`
	Success  int    `json:"success"`
	ItemType *int   `json:"item_type"`
	Type     *int   `json:"type"`
	Name     string `json:"name"`
}
type SteamResponse struct {
	Response struct {
		Items []SteamItem `json:"store_items"`
	} `json:"response"`
}

// GameProof is one store listing that classifies the Discord application.
type GameProof struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	URL    string `json:"url"`
}

// Steam Store Browse app types. Demos, mods, DLC, playtests and multiplayer
// sub-apps belong to a game: Discord often links them next to the base game
// (Darktide lists its "Imperial Edition Upgrade" DLC). Only explicitly
// non-game types veto; anything unlisted is neither proof nor veto.
var steamGameTypes = map[int]string{0: "game", 1: "demo", 2: "mod", 4: "dlc", 12: "beta", 14: "game_component"}
var steamNonGameTypes = map[int]string{3: "movie", 6: "software", 7: "video", 13: "tool"}

// parseSteam: the store browse response explicitly distinguishes type 0 (game)
// from type 6 (software). Unknown/missing types never default to game; pointers are deliberate.
func parseSteam(b []byte) ([]SteamItem, error) {
	var r SteamResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Response.Items == nil {
		return nil, fmt.Errorf("missing Steam store_items")
	}
	seen := map[int]bool{}
	for _, item := range r.Response.Items {
		if item.ID <= 0 || seen[item.ID] {
			return nil, fmt.Errorf("invalid/duplicate Steam item ID")
		}
		seen[item.ID] = true
		if item.Success == 1 && (item.AppID <= 0 || item.ItemType == nil || *item.ItemType != 0 || item.Type == nil || item.Name == "") {
			return nil, fmt.Errorf("invalid Steam application schema for %d", item.ID)
		}
	}
	return r.Response.Items, nil
}
func loadSteam(ids []int, file string) ([]SteamItem, error) {
	if file != "" {
		b, err := readSource(file, "")
		if err != nil {
			return nil, err
		}
		return parseSteam(b)
	}
	all := []SteamItem{}
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		batch := []map[string]int{}
		wanted := map[int]bool{}
		for _, id := range ids[start:end] {
			batch = append(batch, map[string]int{"appid": id})
			wanted[id] = true
		}
		request := map[string]any{"ids": batch, "context": map[string]string{"language": "english", "country_code": "US"}}
		raw, _ := json.Marshal(request)
		b, err := readSource("", steamURL+"?input_json="+url.QueryEscape(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("Steam batch %d: %w", start/100+1, err)
		}
		items, err := parseSteam(b)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if !wanted[item.ID] {
				return nil, fmt.Errorf("unexpected Steam item %d", item.ID)
			}
			delete(wanted, item.ID)
		}
		if len(wanted) != 0 {
			return nil, fmt.Errorf("incomplete Steam response")
		}
		all = append(all, items...)
		fmt.Fprintf(os.Stderr, "Classified Steam batch %d/%d\n", start/100+1, (len(ids)+99)/100)
		if end < len(ids) {
			time.Sleep(350 * time.Millisecond)
		}
	}
	return all, nil
}

// Microsoft Store display catalog: ProductKind "Game" vs "Application".
type XboxProduct struct {
	ID    string `json:"ProductId"`
	Kind  string `json:"ProductKind"`
	Props []struct {
		Title string `json:"ProductTitle"`
	} `json:"LocalizedProperties"`
}
type XboxResponse struct {
	Products []XboxProduct `json:"Products"`
}

var xboxID = regexp.MustCompile(`^[0-9A-Z]{12}$`)

func parseXbox(b []byte) ([]XboxProduct, error) {
	var r XboxResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Products == nil {
		return nil, fmt.Errorf("missing Microsoft Store Products")
	}
	seen := map[string]bool{}
	for _, p := range r.Products {
		if !xboxID.MatchString(p.ID) || seen[p.ID] || p.Kind == "" {
			return nil, fmt.Errorf("invalid Microsoft Store product schema")
		}
		seen[p.ID] = true
	}
	return r.Products, nil
}
func loadXbox(ids []string, file string) ([]XboxProduct, error) {
	if file != "" {
		b, err := readSource(file, "")
		if err != nil {
			return nil, err
		}
		return parseXbox(b)
	}
	all := []XboxProduct{}
	for start := 0; start < len(ids); start += 50 {
		end := min(start+50, len(ids))
		q := url.Values{"bigIds": {strings.Join(ids[start:end], ",")}, "market": {"US"}, "languages": {"en-US"}}
		b, err := readSource("", xboxURL+"?"+q.Encode())
		if err != nil {
			return nil, fmt.Errorf("Microsoft Store batch %d: %w", start/50+1, err)
		}
		items, err := parseXbox(b)
		if err != nil {
			return nil, err
		}
		wanted := map[string]bool{}
		for _, id := range ids[start:end] {
			wanted[id] = true
		}
		for _, p := range items {
			// Missing products are simply absent (delisted/regional); extras are not.
			if !wanted[p.ID] {
				return nil, fmt.Errorf("unexpected Microsoft Store product %s", p.ID)
			}
		}
		all = append(all, items...)
		fmt.Fprintf(os.Stderr, "Classified Microsoft Store batch %d/%d\n", start/50+1, (len(ids)+49)/50)
		if end < len(ids) {
			time.Sleep(350 * time.Millisecond)
		}
	}
	return all, nil
}

type appClass struct {
	game    bool
	nonGame bool
	proofs  []GameProof
}
type sourceOptions struct {
	steamFile, saveSteam, xboxFile, saveXbox string
}
type classified struct {
	names, paths, unknown []Candidate
	counts                map[string]int
	steamHash, xboxHash   string
}

func snapshotHash(v any, save string) (string, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	if save != "" {
		if err = os.WriteFile(save, raw, 0644); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// classifyApps resolves every needed Discord application through Steam and,
// for applications Steam could not settle, through the Microsoft Store.
func classifyApps(apps []App, needed map[string]bool, opt sourceOptions) (map[string]*appClass, string, string, error) {
	steamLinks := map[string][]int{}
	xboxLinks := map[string][]string{}
	uniqueSteam := map[int]bool{}
	for _, app := range apps {
		if !needed[app.ID] {
			continue
		}
		for _, sku := range app.SKUs {
			id := strings.TrimSpace(sku.ID)
			switch sku.Distributor {
			case "steam":
				n, err := strconv.Atoi(id)
				if err != nil || n <= 0 {
					continue // Discord has null/empty/garbage placeholders: no evidence.
				}
				steamLinks[app.ID] = append(steamLinks[app.ID], n)
				uniqueSteam[n] = true
			case "xbox", "microsoft":
				if xboxID.MatchString(strings.ToUpper(id)) {
					xboxLinks[app.ID] = append(xboxLinks[app.ID], strings.ToUpper(id))
				}
			}
		}
	}
	ids := []int{}
	for id := range uniqueSteam {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	items, err := loadSteam(ids, opt.steamFile)
	if err != nil {
		return nil, "", "", err
	}
	byID := map[int]SteamItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	snap := SteamResponse{}
	snap.Response.Items = items
	steamHash, err := snapshotHash(snap, opt.saveSteam)
	if err != nil {
		return nil, "", "", err
	}
	classes := map[string]*appClass{}
	for id := range needed {
		c := &appClass{}
		classes[id] = c
		for _, sid := range steamLinks[id] {
			item, ok := byID[sid]
			if !ok {
				return nil, "", "", fmt.Errorf("Steam snapshot missing requested ID %d", sid)
			}
			// Unavailable listings and aliases are no evidence either way.
			if item.Success != 1 || item.AppID != item.ID {
				continue
			}
			if kind, ok := steamNonGameTypes[*item.Type]; ok {
				c.nonGame = true
				c.proofs = append(c.proofs, GameProof{"steam", strconv.Itoa(sid), item.Name, kind, "https://store.steampowered.com/app/" + strconv.Itoa(sid) + "/"})
			} else if kind, ok := steamGameTypes[*item.Type]; ok {
				c.game = true
				c.proofs = append(c.proofs, GameProof{"steam", strconv.Itoa(sid), item.Name, kind, "https://store.steampowered.com/app/" + strconv.Itoa(sid) + "/"})
			}
		}
	}
	// Only applications without any Steam verdict are sent to the Microsoft Store.
	xset := map[string]bool{}
	for id, c := range classes {
		if !c.game && !c.nonGame {
			for _, x := range xboxLinks[id] {
				xset[x] = true
			}
		}
	}
	xids := []string{}
	for x := range xset {
		xids = append(xids, x)
	}
	sort.Strings(xids)
	products := []XboxProduct{}
	if len(xids) > 0 || opt.xboxFile != "" {
		if products, err = loadXbox(xids, opt.xboxFile); err != nil {
			return nil, "", "", err
		}
	}
	xboxHash, err := snapshotHash(XboxResponse{products}, opt.saveXbox)
	if err != nil {
		return nil, "", "", err
	}
	byX := map[string]XboxProduct{}
	for _, p := range products {
		byX[p.ID] = p
	}
	for id, c := range classes {
		if c.game || c.nonGame {
			continue
		}
		for _, x := range xboxLinks[id] {
			p, ok := byX[x]
			if !ok {
				continue
			}
			title := ""
			if len(p.Props) > 0 {
				title = p.Props[0].Title
			}
			proof := GameProof{"microsoft_store", x, title, strings.ToLower(p.Kind), "https://apps.microsoft.com/detail/" + strings.ToLower(x)}
			switch p.Kind {
			case "Game":
				c.game = true
				c.proofs = append(c.proofs, proof)
			case "Application":
				c.nonGame = true
				c.proofs = append(c.proofs, proof)
			}
		}
	}
	return classes, steamHash, xboxHash, nil
}

// decide applies one rule to every candidate: any owning application that is
// classified as software vetoes the name; otherwise one verified game owner is
// enough. A name shared by a game and its demo/edition is therefore accepted,
// while a name shared with a non-game is not.
func decide(list map[string]*Candidate, classes map[string]*appClass, counts map[string]int, prefix string) (games, unknown []Candidate) {
	keys := []string{}
	for k := range list {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := *list[k]
		sort.Slice(c.Evidence, func(i, j int) bool {
			if c.Evidence[i].ID != c.Evidence[j].ID {
				return c.Evidence[i].ID < c.Evidence[j].ID
			}
			return c.Evidence[i].Path < c.Evidence[j].Path
		})
		owners := map[string]bool{}
		game, nonGame := false, false
		seenProof := map[GameProof]bool{}
		for _, e := range c.Evidence {
			owners[e.ID] = true
			cl := classes[e.ID]
			game = game || cl.game
			nonGame = nonGame || cl.nonGame
			for _, p := range cl.proofs {
				if !seenProof[p] {
					seenProof[p] = true
					c.Games = append(c.Games, p)
				}
			}
		}
		switch {
		case nonGame:
			counts[prefix+"non_game_or_conflicting_type"]++
		case game:
			if len(owners) > 1 {
				counts[prefix+"shared_by_game_apps"]++
			}
			games = append(games, c)
		default:
			counts[prefix+"unverified"]++
			unknown = append(unknown, c)
		}
	}
	return games, unknown
}
func classify(apps []App, found collected, opt sourceOptions) (classified, error) {
	needed := map[string]bool{}
	for _, m := range []map[string]*Candidate{found.names, found.paths} {
		for _, c := range m {
			for _, e := range c.Evidence {
				needed[e.ID] = true
			}
		}
	}
	classes, steamHash, xboxHash, err := classifyApps(apps, needed, opt)
	if err != nil {
		return classified{}, err
	}
	counts := map[string]int{}
	names, unknown := decide(found.names, classes, counts, "")
	paths, unknownPaths := decide(found.paths, classes, counts, "path_")
	counts["games"] = len(names)
	counts["path_games"] = len(paths)
	return classified{names, paths, append(unknown, unknownPaths...), counts, steamHash, xboxHash}, nil
}
