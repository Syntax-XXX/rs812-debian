// rs812-panel: schlankes Backend für die CasaOS-Oberfläche auf der Synology RS812 (Debian, ARMv5).
// Bedient die von CasaOS-UI erwarteten REST-Schnittstellen ({success, message, data}) und einen
// minimalen Socket.IO-v2-Endpunkt für Live-Werte. Docker/App-Store gibt es hier nicht.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	listen  = flag.String("listen", ":80", "Adresse")
	wwwDir  = flag.String("www", "/usr/share/rs812-panel/www", "gebaute CasaOS-UI")
	dataDir = flag.String("data", "/var/lib/rs812-panel", "Zustand (Benutzer, Einstellungen)")
	rootDir = flag.String("files-root", "/", "Wurzel für den Datei-Manager")
	debug   = flag.Bool("debug", false, "unbekannte Aufrufe protokollieren")
)

type result struct {
	Success int    `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func ok(w http.ResponseWriter, data any) { writeJSON(w, 200, result{200, "ok", data}) }

func fail(w http.ResponseWriter, status, code int, msg string) {
	writeJSON(w, status, result{code, msg, nil})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func main() {
	flag.Parse()
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatal(err)
	}
	store := loadStore(filepath.Join(*dataDir, "state.json"))
	mux := http.NewServeMux()
	registerUsers(mux, store)
	registerSys(mux, store)
	registerFiles(mux, store)
	registerStorage(mux, store)
	registerApps(mux, store)
	registerMessageBus(mux, store)

	// Unbekannte API-Aufrufe: leer, aber erfolgreich beantworten, damit die Oberfläche nicht hängt.
	api := func(w http.ResponseWriter, r *http.Request) {
		if *debug {
			log.Printf("UNBEKANNT %s %s", r.Method, r.URL.String())
		}
		ok(w, nil)
	}
	mux.HandleFunc("/v1/", api)
	mux.HandleFunc("/v2/", api)

	// Oberfläche (statisch, Hash-Router → index.html)
	fs := http.FileServer(http.Dir(*wwwDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(*wwwDir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() && r.URL.Path != "/" {
			r.URL.Path = "/"
		}
		if strings.HasSuffix(r.URL.Path, ".html") || r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fs.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 15 * time.Second,
	}
	log.Printf("rs812-panel auf %s (www=%s, daten=%s)", *listen, *wwwDir, *dataDir)
	log.Fatal(srv.ListenAndServe())
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *debug && (strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v2/")) {
			log.Printf("%s %s", r.Method, r.URL.String())
		}
		h.ServeHTTP(w, r)
	})
}
