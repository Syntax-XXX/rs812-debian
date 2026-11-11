package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ── App-Kacheln: CasaOS-App-Grid ohne Docker; Link-Apps kommen aus dem Benutzer-Speicher ("link") ──
func registerApps(mux *http.ServeMux, s *storeT) {
	empty := func(w http.ResponseWriter, r *http.Request) { ok(w, []any{}) }
	mux.HandleFunc("GET /v2/app_management/web/appgrid", empty)
	mux.HandleFunc("GET /v2/app_management/compose", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{}) })
	mux.HandleFunc("GET /v2/app_management/apps", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"list": map[string]any{}}) })
	mux.HandleFunc("GET /v2/app_management/categories", empty)
	// Docker gibt es auf der RS812 nicht: leere, aber gültige Antworten für die Oberfläche
	mux.HandleFunc("GET /v1/container", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"casaos_apps": []any{}, "local_apps": []any{}})
	})
	mux.HandleFunc("GET /v1/container/usage", empty)
	mux.HandleFunc("GET /v2/users/events", empty)
	mux.HandleFunc("GET /v1/gateway/port", func(w http.ResponseWriter, r *http.Request) { ok(w, "80") })
	mux.HandleFunc("GET /v1/usb/usb-auto-mount", func(w http.ResponseWriter, r *http.Request) { ok(w, "False") })
	mux.HandleFunc("GET /v1/driver", empty) // keine Cloud-Laufwerke
	mux.HandleFunc("GET /v1/cloud", empty)
	mux.HandleFunc("GET /v1/samba/connections", empty)
	registerAdmin(mux, s)
}

// ── Hintergrund-Jobs (apt, Neustart …) mit mitlaufender Ausgabe ──
type job struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Running  bool      `json:"running"`
	ExitCode int       `json:"exit_code"`
	Started  time.Time `json:"started"`
	out      bytes.Buffer
	mu       sync.Mutex
}

func (j *job) Write(b []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.out.Len() > 2<<20 { // höchstens 2 MB behalten
		j.out.Reset()
		j.out.WriteString("… (gekürzt)\n")
	}
	return j.out.Write(b)
}

var jobs = struct {
	sync.Mutex
	m map[string]*job
}{m: map[string]*job{}}

func startJob(title string, name string, args ...string) *job {
	id := make([]byte, 6)
	_, _ = rand.Read(id)
	j := &job{ID: hex.EncodeToString(id), Title: title, Running: true, Started: time.Now()}
	jobs.Lock()
	jobs.m[j.ID] = j
	jobs.Unlock()
	go func() {
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LANG=C.UTF-8")
		cmd.Stdout, cmd.Stderr = j, j
		err := cmd.Run()
		j.mu.Lock()
		j.Running = false
		if ee, ok := err.(*exec.ExitError); ok {
			j.ExitCode = ee.ExitCode()
		} else if err != nil {
			j.ExitCode = -1
			fmt.Fprintf(&j.out, "\nFehler: %v\n", err)
		}
		j.mu.Unlock()
	}()
	return j
}

var aptBusy sync.Mutex

func run(name string, args ...string) (string, error) {
	b, err := exec.Command(name, args...).CombinedOutput()
	return string(b), err
}

var svcName = regexp.MustCompile(`^[a-zA-Z0-9@._-]+\.service$`)

// Dienste, die im Panel auftauchen (der Rest ist Systeminternes)
var listedServices = []string{"ssh.service", "nfs-server.service", "rs812-fan.service", "rs812-panel.service",
	"smartmontools.service", "systemd-timesyncd.service", "cron.service", "mdmonitor.service", "smbd.service"}

func registerAdmin(mux *http.ServeMux, s *storeT) {
	// Dienste
	mux.HandleFunc("GET /v1/rs812/services", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var list []map[string]any
		for _, n := range listedServices {
			out, _ := run("systemctl", "show", n, "--no-pager", "-p", "LoadState,ActiveState,SubState,UnitFileState,Description")
			m := map[string]any{"name": n}
			for _, l := range strings.Split(out, "\n") {
				if k, v, okk := strings.Cut(l, "="); okk {
					m[k] = v
				}
			}
			if m["LoadState"] == "not-found" {
				continue
			}
			list = append(list, m)
		}
		ok(w, list)
	}))
	mux.HandleFunc("POST /v1/rs812/services/{name}/{action}", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		n, a := r.PathValue("name"), r.PathValue("action")
		if !svcName.MatchString(n) || !map[string]bool{"start": true, "stop": true, "restart": true, "enable": true, "disable": true}[a] {
			fail(w, 400, 400, "ungültig")
			return
		}
		if n == "rs812-panel.service" && (a == "stop" || a == "disable") {
			fail(w, 400, 400, "Das Panel kann sich nicht selbst abschalten")
			return
		}
		if n == "rs812-panel.service" && a == "restart" {
			ok(w, "Neustart in 1 s")
			go func() { time.Sleep(time.Second); _, _ = run("systemctl", "restart", n) }()
			return
		}
		out, err := run("systemctl", a, n)
		if err != nil {
			fail(w, 200, 500, strings.TrimSpace(out))
			return
		}
		ok(w, "ok")
	}))

	// NFS-Freigaben (/etc/exports) lesen und schreiben
	mux.HandleFunc("GET /v1/rs812/exports", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		b, _ := os.ReadFile("/etc/exports")
		active, _ := run("exportfs", "-v")
		ok(w, map[string]any{"content": string(b), "active": active})
	}))
	mux.HandleFunc("PUT /v1/rs812/exports", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var in map[string]string
		if readJSON(r, &in) != nil {
			fail(w, 400, 400, "ungültig")
			return
		}
		old, _ := os.ReadFile("/etc/exports")
		_ = os.WriteFile("/etc/exports.bak", old, 0o644)
		if err := os.WriteFile("/etc/exports", []byte(in["content"]), 0o644); err != nil {
			fail(w, 200, 500, err.Error())
			return
		}
		if out, err := run("exportfs", "-ra"); err != nil {
			_ = os.WriteFile("/etc/exports", old, 0o644) // bei Fehler alte Fassung zurück
			_, _ = run("exportfs", "-ra")
			fail(w, 200, 500, "Fehler, alte Freigaben wiederhergestellt: "+strings.TrimSpace(out))
			return
		}
		ok(w, "übernommen")
	}))

	// Updates: Liste der verfügbaren Pakete, dann apt upgrade als Job
	mux.HandleFunc("POST /v1/rs812/updates/check", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		if !aptBusy.TryLock() {
			fail(w, 200, 409, "apt läuft bereits")
			return
		}
		defer aptBusy.Unlock()
		_, _ = run("apt-get", "update", "-qq")
		out, _ := run("apt", "list", "--upgradable")
		var pk []string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "upgradable from") {
				pk = append(pk, l)
			}
		}
		ok(w, pk)
	}))
	mux.HandleFunc("POST /v1/rs812/updates/install", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		j := startJob("Updates installieren", "sh", "-c", "apt-get update -q && apt-get -y -o Dpkg::Options::=--force-confold upgrade")
		ok(w, j.ID)
	}))
	mux.HandleFunc("POST /v1/rs812/apt/install", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var in map[string]string
		_ = readJSON(r, &in)
		pkgs := strings.Fields(in["packages"])
		for _, p := range pkgs {
			if !regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`).MatchString(p) {
				fail(w, 400, 400, "ungültiger Paketname: "+p)
				return
			}
		}
		if len(pkgs) == 0 {
			fail(w, 400, 400, "keine Pakete")
			return
		}
		j := startJob("Installieren: "+strings.Join(pkgs, " "), "apt-get", append([]string{"install", "-y"}, pkgs...)...)
		ok(w, j.ID)
	}))
	mux.HandleFunc("GET /v1/rs812/jobs/{id}", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		jobs.Lock()
		j := jobs.m[r.PathValue("id")]
		jobs.Unlock()
		if j == nil {
			fail(w, 404, 404, "unbekannt")
			return
		}
		j.mu.Lock()
		defer j.mu.Unlock()
		ok(w, map[string]any{"id": j.ID, "title": j.Title, "running": j.Running, "exit_code": j.ExitCode, "output": j.out.String()})
	}))

	// Protokolle
	mux.HandleFunc("GET /v1/rs812/logs", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		unit := r.URL.Query().Get("unit")
		args := []string{"-n", "400", "--no-pager", "-o", "short-iso"}
		if unit != "" {
			if !svcName.MatchString(unit) {
				fail(w, 400, 400, "ungültig")
				return
			}
			args = append(args, "-u", unit)
		}
		if r.URL.Query().Get("kernel") == "1" {
			args = append(args, "-k")
		}
		out, _ := run("journalctl", args...)
		ok(w, out)
	}))

	// Energie
	mux.HandleFunc("POST /v1/rs812/power/{action}", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		a := r.PathValue("action")
		if a != "reboot" && a != "poweroff" {
			fail(w, 400, 400, "ungültig")
			return
		}
		ok(w, a)
		go func() { time.Sleep(2 * time.Second); _, _ = run("systemctl", a) }()
	}))
	// CasaOS-Oberfläche: Neustart/Herunterfahren aus dem Menü
	mux.HandleFunc("PUT /v1/sys/state/{st}", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		switch r.PathValue("st") {
		case "restart":
			ok(w, "")
			go func() { time.Sleep(2 * time.Second); _, _ = run("systemctl", "reboot") }()
		case "off":
			ok(w, "")
			go func() { time.Sleep(2 * time.Second); _, _ = run("systemctl", "poweroff") }()
		default:
			fail(w, 400, 400, "ungültig")
		}
	}))
}
