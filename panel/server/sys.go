package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ── Messwerte aus /proc ──────────────────────────────────────────────────────

var cpuPrev struct {
	sync.Mutex
	idle, total uint64
	percent     float64
}

// cpuSample: Auslastung seit dem letzten Aufruf (wird vom Sammler alle 3 s aufgerufen)
func cpuSample() float64 {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
	var total, idle uint64
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	cpuPrev.Lock()
	defer cpuPrev.Unlock()
	if cpuPrev.total > 0 && total > cpuPrev.total {
		dt, di := total-cpuPrev.total, idle-cpuPrev.idle
		cpuPrev.percent = float64(int(1000*float64(dt-di)/float64(dt))) / 10
	}
	cpuPrev.idle, cpuPrev.total = idle, total
	return cpuPrev.percent
}

func memInfo() map[string]any {
	m := map[string]uint64{}
	f, err := os.Open("/proc/meminfo")
	if err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			p := strings.Fields(sc.Text())
			if len(p) >= 2 {
				n, _ := strconv.ParseUint(p[1], 10, 64)
				m[strings.TrimSuffix(p[0], ":")] = n * 1024
			}
		}
		f.Close()
	}
	total, avail := m["MemTotal"], m["MemAvailable"]
	used := total - avail
	pct := 0.0
	if total > 0 {
		pct = float64(int(1000*float64(used)/float64(total))) / 10
	}
	return map[string]any{"total": total, "available": avail, "used": used, "free": m["MemFree"],
		"usedPercent": pct, "swapTotal": m["SwapTotal"], "swapFree": m["SwapFree"]}
}

type netStat struct {
	Name        string `json:"name"`
	BytesSent   uint64 `json:"bytesSent"`
	BytesRecv   uint64 `json:"bytesRecv"`
	PacketsSent uint64 `json:"packetsSent"`
	PacketsRecv uint64 `json:"packetsRecv"`
	State       string `json:"state"`
	Time        int64  `json:"time"`
}

func netStats() []netStat {
	out := []netStat{}
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n")[2:] {
		p := strings.Fields(strings.Replace(l, ":", " ", 1))
		if len(p) < 17 || !strings.HasPrefix(p[0], "eth") {
			continue
		}
		u := func(i int) uint64 { n, _ := strconv.ParseUint(p[i], 10, 64); return n }
		st, _ := os.ReadFile("/sys/class/net/" + p[0] + "/operstate")
		out = append(out, netStat{p[0], u(9), u(1), u(10), u(2), strings.TrimSpace(string(st)), time.Now().Unix()})
	}
	return out
}

// volumes: alle eingehängten /volumeN (Daten) – Größe/Belegung in Bytes
type volume struct {
	Path  string `json:"path"`
	Dev   string `json:"dev"`
	Size  uint64 `json:"size"`
	Used  uint64 `json:"used"`
	Avail uint64 `json:"avail"`
}

func volumes() []volume {
	out := []volume{}
	b, _ := os.ReadFile("/proc/mounts")
	for _, l := range strings.Split(string(b), "\n") {
		p := strings.Fields(l)
		if len(p) < 2 || !strings.HasPrefix(p[1], "/volume") {
			continue
		}
		var s syscall.Statfs_t
		if syscall.Statfs(p[1], &s) != nil {
			continue
		}
		bs := uint64(s.Bsize)
		out = append(out, volume{p[1], p[0], s.Blocks * bs, (s.Blocks - s.Bfree) * bs, s.Bavail * bs})
	}
	return out
}

// ── Platten: SMART (ohne schlafende Platten zu wecken), alle 60 s aufgefrischt ──
type diskInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Model    string `json:"model"`
	Serial   string `json:"serial"`
	Size     uint64 `json:"size"`
	Temp     int    `json:"temperature"`
	Health   bool   `json:"health"`
	Standby  bool   `json:"standby"`
	Hours    int    `json:"power_on_hours"`
	Realloc  int    `json:"reallocated"`
	Pending  int    `json:"pending"`
	Rotation string `json:"rotation"`
}

var disksCache struct {
	sync.Mutex
	list []diskInfo
	at   time.Time
}

func readDisks() []diskInfo {
	disksCache.Lock()
	defer disksCache.Unlock()
	if time.Since(disksCache.at) < 60*time.Second && disksCache.list != nil {
		return disksCache.list
	}
	var out []diskInfo
	ents, _ := os.ReadDir("/sys/block")
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(n, "sd") {
			continue
		}
		d := diskInfo{Name: n, Path: "/dev/" + n, Health: true}
		if b, err := os.ReadFile("/sys/block/" + n + "/size"); err == nil {
			s, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
			d.Size = s * 512
		}
		if b, err := os.ReadFile("/sys/block/" + n + "/device/model"); err == nil {
			d.Model = strings.TrimSpace(string(b))
		}
		// -n standby: schlafende Platte nicht aufwecken (Exit-Code 2)
		cmd := exec.Command("smartctl", "-n", "standby", "-j", "-H", "-i", "-A", d.Path)
		raw, err := cmd.Output()
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode()&2 != 0 && len(raw) < 200 {
			d.Standby = true
		}
		var sm struct {
			SerialNumber string `json:"serial_number"`
			ModelName    string `json:"model_name"`
			RotationRate int    `json:"rotation_rate"`
			SmartStatus  *struct {
				Passed bool `json:"passed"`
			} `json:"smart_status"`
			Temperature struct {
				Current int `json:"current"`
			} `json:"temperature"`
			PowerOnTime struct {
				Hours int `json:"hours"`
			} `json:"power_on_time"`
			Ata struct {
				Table []struct {
					ID  int `json:"id"`
					Raw struct {
						Value int `json:"value"`
					} `json:"raw"`
				} `json:"table"`
			} `json:"ata_smart_attributes"`
		}
		if json.Unmarshal(raw, &sm) == nil {
			if sm.ModelName != "" {
				d.Model = sm.ModelName
			}
			d.Serial = sm.SerialNumber
			d.Temp = sm.Temperature.Current
			d.Hours = sm.PowerOnTime.Hours
			if sm.SmartStatus != nil {
				d.Health = sm.SmartStatus.Passed
			}
			if sm.RotationRate > 0 {
				d.Rotation = strconv.Itoa(sm.RotationRate) + " U/min"
			}
			for _, a := range sm.Ata.Table {
				switch a.ID {
				case 5:
					d.Realloc = a.Raw.Value
				case 197:
					d.Pending = a.Raw.Value
				}
			}
			if d.Realloc > 0 || d.Pending > 0 {
				d.Health = false
			}
		}
		out = append(out, d)
	}
	disksCache.list, disksCache.at = out, time.Now()
	return out
}

func maxDiskTemp() int {
	t := 0
	for _, d := range readDisks() {
		if d.Temp > t {
			t = d.Temp
		}
	}
	return t
}

func fanInfo() map[string]any {
	out := map[string]any{"rpm": 0, "pwm": 0}
	ents, _ := os.ReadDir("/sys/class/hwmon")
	for _, e := range ents {
		base := "/sys/class/hwmon/" + e.Name() + "/"
		if n, _ := os.ReadFile(base + "name"); strings.TrimSpace(string(n)) == "gpio_fan" {
			rd := func(f string) int { b, _ := os.ReadFile(base + f); v, _ := strconv.Atoi(strings.TrimSpace(string(b))); return v }
			out["rpm"], out["pwm"], out["max"] = rd("fan1_input"), rd("pwm1"), rd("fan1_max")
		}
	}
	return out
}

// sysDisk: Summe aller Daten-Volumes – so erwartet es das Speicher-Widget
func sysDisk() map[string]any {
	var size, used, avail uint64
	for _, v := range volumes() {
		size, used, avail = size+v.Size, used+v.Used, avail+v.Avail
	}
	health := true
	for _, d := range readDisks() {
		if !d.Health {
			health = false
		}
	}
	return map[string]any{"size": size, "used": used, "avail": avail, "health": health}
}

func cpuData() map[string]any {
	return map[string]any{"percent": cpuPrev.percent, "num": runtime.NumCPU(), "temperature": maxDiskTemp(),
		"power": map[string]any{"timestamp": time.Now().Unix(), "value": "0"}, "model": "arm"}
}

func utilization() map[string]any {
	return map[string]any{"cpu": cpuData(), "mem": memInfo(), "net": netStats(),
		"sys_disk": sysDisk(), "sys_usb": []any{}}
}

func hostname() string { h, _ := os.Hostname(); return h }

func uptimeSeconds() int64 {
	b, _ := os.ReadFile("/proc/uptime")
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}

func loadAvg() string {
	b, _ := os.ReadFile("/proc/loadavg")
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return ""
	}
	return strings.Join(f[:3], " ")
}

func registerSys(mux *http.ServeMux, s *storeT) {
	go func() { // CPU-Messung im Hintergrund, damit die Prozentwerte stimmen
		for {
			cpuSample()
			time.Sleep(3 * time.Second)
		}
	}()
	mux.HandleFunc("GET /v1/sys/utilization", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, utilization()) }))
	mux.HandleFunc("GET /v1/sys/cpu", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, cpuData()) }))
	mux.HandleFunc("GET /v1/sys/mem", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, memInfo()) }))
	mux.HandleFunc("GET /v1/sys/network", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, netStats()) }))
	mux.HandleFunc("GET /v1/sys/disk", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) { ok(w, sysDisk()) }))
	mux.HandleFunc("GET /v1/sys/hardware", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		ok(w, map[string]any{"drive_model": "Synology RS812", "arch": "arm", "hostname": hostname()})
	}))
	mux.HandleFunc("GET /v1/sys/version", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"need_update": false, "version": map[string]any{"version": "0.4.15", "change_log": ""}, "current_version": "0.4.15"})
	})
	mux.HandleFunc("GET /v1/sys/state", func(w http.ResponseWriter, r *http.Request) { ok(w, "") })
	mux.HandleFunc("GET /v1/sys/apps-state", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{}) })
	mux.HandleFunc("GET /v1/sys/logs", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		b, _ := exec.Command("journalctl", "-n", "300", "--no-pager", "-o", "short-iso").Output()
		ok(w, string(b))
	}))
	// Eigene Erweiterungen fürs RS812-Panel
	mux.HandleFunc("GET /v1/rs812/overview", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		ok(w, map[string]any{"hostname": hostname(), "uptime": uptimeSeconds(), "load": loadAvg(),
			"fan": fanInfo(), "disks": readDisks(), "volumes": volumes(), "raid": mdstat(), "net": netStats(), "mem": memInfo()})
	}))
}
