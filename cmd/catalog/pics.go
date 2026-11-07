package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Steam PICS app info (launch configuration) through the public steamcmd.net
// mirror. It supplies executables for Steam games that Discord lists without
// any Windows path: config.installdir + config.launch.*.executable.
const picsURL = "https://api.steamcmd.net/v1/info/"
const picsWorkers = 4

// PicsApp is the compact, reviewable snapshot of one app's launch data.
type PicsApp struct {
	ID         int      `json:"appid"`
	Type       string   `json:"type"`
	InstallDir string   `json:"installdir"`
	Launch     []string `json:"windows_executables"`
}

type picsLaunch struct {
	Executable string `json:"executable"`
	Config     struct {
		OSList string `json:"oslist"`
	} `json:"config"`
}
type picsEntry struct {
	Common *struct {
		Type string `json:"type"`
	} `json:"common"`
	Config *struct {
		InstallDir string                `json:"installdir"`
		Launch     map[string]picsLaunch `json:"launch"`
	} `json:"config"`
}

func parsePics(id int, b []byte) (PicsApp, error) {
	var r struct {
		Status string                     `json:"status"`
		Data   map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return PicsApp{}, err
	}
	raw, ok := r.Data[strconv.Itoa(id)]
	if r.Status != "success" || !ok {
		return PicsApp{}, fmt.Errorf("invalid Steam PICS response for %d", id)
	}
	var e picsEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return PicsApp{}, err
	}
	app := PicsApp{ID: id, Launch: []string{}}
	if e.Common == nil || e.Config == nil {
		return app, nil // Unknown/private app: no evidence.
	}
	app.Type = strings.ToLower(e.Common.Type)
	app.InstallDir = e.Config.InstallDir
	keys := []string{}
	for k := range e.Config.Launch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, k := range keys {
		l := e.Config.Launch[k]
		exe := strings.TrimPrefix(strings.ReplaceAll(l.Executable, "\\", "/"), "./")
		os := strings.ToLower(l.Config.OSList)
		if exe == "" || (os != "" && !strings.Contains(os, "windows")) || !strings.HasSuffix(strings.ToLower(exe), ".exe") || seen[exe] {
			continue
		}
		seen[exe] = true
		app.Launch = append(app.Launch, exe)
	}
	return app, nil
}

func loadPics(ids []int, file, save string) (map[int]PicsApp, string, error) {
	out := map[int]PicsApp{}
	if file != "" {
		b, err := readSource(file, "")
		if err != nil {
			return nil, "", err
		}
		var list []PicsApp
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, "", err
		}
		for _, a := range list {
			if a.ID <= 0 || a.Launch == nil {
				return nil, "", fmt.Errorf("invalid Steam PICS snapshot entry")
			}
			out[a.ID] = a
		}
		for _, id := range ids {
			if _, ok := out[id]; !ok {
				return nil, "", fmt.Errorf("Steam PICS snapshot missing requested ID %d", id)
			}
		}
	} else if len(ids) > 0 {
		var mu sync.Mutex
		var first error
		next := make(chan int)
		var wg sync.WaitGroup
		done := 0
		for w := 0; w < picsWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := range next {
					b, err := readSource("", picsURL+strconv.Itoa(id))
					var app PicsApp
					if err == nil {
						app, err = parsePics(id, b)
					}
					mu.Lock()
					if err != nil && first == nil {
						first = fmt.Errorf("Steam PICS %d: %w", id, err)
					}
					out[id] = app
					done++
					if done%1000 == 0 {
						fmt.Fprintf(os.Stderr, "Steam PICS %d/%d\n", done, len(ids))
					}
					mu.Unlock()
					time.Sleep(50 * time.Millisecond)
				}
			}()
		}
		for _, id := range ids {
			mu.Lock()
			failed := first != nil
			mu.Unlock()
			if failed {
				break
			}
			next <- id
		}
		close(next)
		wg.Wait()
		if first != nil {
			return nil, "", first
		}
	}
	list := []PicsApp{}
	for _, id := range ids {
		list = append(list, out[id])
	}
	hash, err := snapshotHash(list, save)
	return out, hash, err
}

// picsTargets: Discord applications without any Windows executable. Only these
// get launch data; for the rest Discord's own detection paths are preferred.
func picsTargets(apps []App) map[string]bool {
	t := map[string]bool{}
	for _, app := range apps {
		win := false
		for _, e := range app.Executables {
			win = win || e.OS == "win32"
		}
		if win {
			continue
		}
		for _, s := range app.SKUs {
			if s.Distributor == "steam" && strings.TrimSpace(s.ID) != "" {
				t[app.ID] = true
			}
		}
	}
	return t
}

func addPics(apps []App, targets map[string]bool, classes map[string]*appClass, col *collector, opt sourceOptions) (string, error) {
	if opt.noPics {
		return "", nil
	}
	unique := map[int]bool{}
	for id := range targets {
		c := classes[id]
		if c == nil || !c.game || c.nonGame {
			continue
		}
		for _, sid := range c.steamGames {
			unique[sid] = true
		}
	}
	ids := []int{}
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	pics, hash, err := loadPics(ids, opt.picsFile, opt.savePics)
	if err != nil {
		return "", err
	}
	for _, app := range apps {
		c := classes[app.ID]
		if !targets[app.ID] || c == nil || !c.game || c.nonGame {
			continue
		}
		for _, sid := range c.steamGames {
			p := pics[sid]
			if p.Type != "game" {
				col.counts["pics_no_game_launch_data"]++
				continue
			}
			for _, exe := range p.Launch {
				full := exe
				if p.InstallDir != "" {
					full = strings.Trim(p.InstallDir, "/\\") + "/" + exe
				}
				col.counts["pics_executables"]++
				col.add(full, Evidence{app.ID, app.Name, full, "steam_pics:" + strconv.Itoa(sid)}, "pics_")
			}
		}
	}
	return hash, nil
}
