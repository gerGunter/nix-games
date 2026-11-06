package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const steamURL = "https://api.steampowered.com/IStoreBrowseService/GetItems/v1/"

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
type GameProof struct {
	AppID int    `json:"steam_appid"`
	Name  string `json:"steam_name"`
	Type  int    `json:"steam_type"`
	URL   string `json:"store_url"`
}

// The store browse response explicitly distinguishes type 0 (game) from type 6
// (software). Unknown/missing types never default to game; pointers are deliberate.
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
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
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
func classify(data []byte, candidates []Candidate, file, save string) ([]Candidate, []Candidate, map[string]int, string, error) {
	var apps []App
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, nil, nil, "", err
	}
	needed := map[string]bool{}
	for _, c := range candidates {
		for _, e := range c.Evidence {
			needed[e.ID] = true
		}
	}
	links := map[string][]int{}
	unique := map[int]bool{}
	for _, app := range apps {
		if !needed[app.ID] {
			continue
		}
		for _, sku := range app.SKUs {
			if sku.Distributor != "steam" {
				continue
			}
			// Discord includes null/empty SKU placeholders alongside real links.
			if sku.ID == "" {
				continue
			}
			id, err := strconv.Atoi(strings.TrimSpace(sku.ID))
			if err != nil || id <= 0 {
				continue // An invalid link is no evidence of a game.
			}
			links[app.ID] = append(links[app.ID], id)
			unique[id] = true
		}
	}
	ids := []int{}
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	items, err := loadSteam(ids, file)
	if err != nil {
		return nil, nil, nil, "", err
	}
	byID := map[int]SteamItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	snapshot := SteamResponse{}
	snapshot.Response.Items = items
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, nil, nil, "", err
	}
	if save != "" {
		if err = os.WriteFile(save, raw, 0644); err != nil {
			return nil, nil, nil, "", err
		}
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	games := []Candidate{}
	unknown := []Candidate{}
	counts := map[string]int{}
	for _, c := range candidates {
		proofs := map[int]GameProof{}
		nonGame := false
		unavailable := false
		for _, e := range c.Evidence {
			for _, id := range links[e.ID] {
				item, ok := byID[id]
				if !ok {
					return nil, nil, nil, "", fmt.Errorf("Steam snapshot missing requested ID %d", id)
				}
				if item.Success != 1 {
					unavailable = true
					continue
				}
				if item.AppID != item.ID {
					unavailable = true
					continue
				} // aliases need separate verification
				if *item.Type != 0 {
					nonGame = true
					continue
				}
				proofs[id] = GameProof{id, item.Name, *item.Type, "https://store.steampowered.com/app/" + strconv.Itoa(id) + "/"}
			}
		}
		if nonGame {
			counts["non_game_or_conflicting_type"]++
			continue
		}
		if len(proofs) == 0 || unavailable {
			counts["unverified"]++
			unknown = append(unknown, c)
			continue
		}
		for _, proof := range proofs {
			c.Games = append(c.Games, proof)
		}
		sort.Slice(c.Games, func(i, j int) bool { return c.Games[i].AppID < c.Games[j].AppID })
		games = append(games, c)
	}
	counts["games"] = len(games)
	return games, unknown, counts, hash, nil
}
