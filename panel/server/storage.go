package main

import (
	"bufio"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type raidInfo struct {
	Name    string   `json:"name"`
	Level   string   `json:"level"`
	State   string   `json:"state"`
	Members []string `json:"members"`
	Status  string   `json:"status"` // z. B. [4/3] [UUU_]
	Mount   string   `json:"mount"`
	Sync    string   `json:"sync,omitempty"`
}

var mdLine = regexp.MustCompile(`^(md\d+)\s*:\s*(\S+)\s+(?:\(\S+\)\s+)?(\S+)\s+(.*)$`)

func mdstat() []raidInfo {
	mounts := map[string]string{}
	if b, err := os.ReadFile("/proc/mounts"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			p := strings.Fields(l)
			if len(p) >= 2 && strings.HasPrefix(p[0], "/dev/md") {
				mounts[strings.TrimPrefix(p[0], "/dev/")] = p[1]
			}
		}
	}
	if b, err := os.ReadFile("/proc/swaps"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			p := strings.Fields(l)
			if len(p) > 0 && strings.HasPrefix(p[0], "/dev/md") {
				mounts[strings.TrimPrefix(p[0], "/dev/")] = "swap"
			}
		}
	}
	var out []raidInfo
	f, err := os.Open("/proc/mdstat")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var cur *raidInfo
	for sc.Scan() {
		l := sc.Text()
		if m := mdLine.FindStringSubmatch(l); m != nil {
			out = append(out, raidInfo{Name: m[1], State: m[2], Level: m[3], Mount: mounts[m[1]]})
			cur = &out[len(out)-1]
			for _, d := range strings.Fields(m[4]) {
				cur.Members = append(cur.Members, regexp.MustCompile(`\[\d+\].*`).ReplaceAllString(d, ""))
			}
			continue
		}
		if cur != nil {
			t := strings.TrimSpace(l)
			if i := strings.Index(t, "["); i >= 0 && strings.Contains(t, "blocks") {
				cur.Status = strings.TrimSpace(t[i:])
			}
			if strings.Contains(t, "recovery") || strings.Contains(t, "resync") {
				cur.Sync = t
			}
		}
	}
	return out
}

func registerStorage(mux *http.ServeMux, s *storeT) {
	// Platten wie die CasaOS-Speicherverwaltung sie anzeigt
	mux.HandleFunc("GET /v1/disks", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var list []map[string]any
		for _, d := range readDisks() {
			list = append(list, map[string]any{"name": d.Name, "path": d.Path, "model": d.Model, "serial": d.Serial,
				"size": d.Size, "temperature": d.Temp, "health": map[bool]string{true: "true", false: "false"}[d.Health],
				"type": "HDD", "disk_type": "HDD", "free": true, "children_number": 3, "support": true})
		}
		ok(w, list)
	}))
	mux.HandleFunc("GET /v1/disks/usb", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, []any{}) }))
	mux.HandleFunc("GET /v1/storage", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var list []map[string]any
		for _, v := range volumes() {
			list = append(list, map[string]any{"mount_point": v.Path, "path": v.Dev, "size": v.Size, "used": v.Used,
				"avail": v.Avail, "label": strings.TrimPrefix(v.Path, "/"), "type": "ext4", "drive_name": v.Dev, "persistent_type": "raid"})
		}
		ok(w, map[string]any{"storages": list, "total": len(list)})
	}))
	mux.HandleFunc("GET /v2/local_storage/mount", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var list []map[string]any
		for _, v := range volumes() {
			list = append(list, map[string]any{"id": 0, "mount_point": v.Path, "source": v.Dev, "fstype": "ext4", "options": "rw"})
		}
		ok(w, list)
	}))
	mux.HandleFunc("GET /v2/local_storage/merge", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, []any{}) }))
	mux.HandleFunc("GET /v2/local_storage/merge/init", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, false) }))
	mux.HandleFunc("GET /v1/rs812/raid", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, mdstat()) }))
	mux.HandleFunc("GET /v1/rs812/smart/{dev}", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		dev := r.PathValue("dev")
		if !regexp.MustCompile(`^sd[a-z]$`).MatchString(dev) {
			fail(w, 400, 400, "ungültig")
			return
		}
		b, _ := exec.Command("smartctl", "-n", "standby", "-a", "/dev/"+dev).CombinedOutput()
		ok(w, string(b))
	}))
}
