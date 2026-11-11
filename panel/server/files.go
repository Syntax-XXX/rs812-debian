package main

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Erlaubte Bereiche für den Datei-Manager (alles andere wird abgelehnt)
var fileRoots = []string{"/volume1", "/volume2", "/volume3", "/home"}

// safePath: absoluter, bereinigter Pfad innerhalb eines erlaubten Bereichs (auch nach Symlink-Auflösung)
func safePath(p string) (string, error) {
	if p == "" {
		return "", errors.New("kein Pfad")
	}
	c := filepath.Clean("/" + p)
	check := c
	if r, err := filepath.EvalSymlinks(c); err == nil {
		check = r
	} else if r, err := filepath.EvalSymlinks(filepath.Dir(c)); err == nil {
		check = filepath.Join(r, filepath.Base(c))
	}
	for _, root := range fileRoots {
		if check == root || strings.HasPrefix(check, root+"/") {
			return c, nil
		}
	}
	return "", fmt.Errorf("Pfad nicht erlaubt: %s", c)
}

func isRoot(p string) bool {
	for _, r := range fileRoots {
		if p == r {
			return true
		}
	}
	return false
}

type fsItem struct {
	Name       string         `json:"name"`
	Path       string         `json:"path"`
	IsDir      bool           `json:"is_dir"`
	Size       int64          `json:"size"`
	Date       time.Time      `json:"date"`
	Modified   time.Time      `json:"modified"`
	Write      bool           `json:"write"`
	Extensions map[string]any `json:"extensions"`
}

func itemOf(path string, fi fs.FileInfo) fsItem {
	return fsItem{Name: fi.Name(), Path: path, IsDir: fi.IsDir(), Size: fi.Size(), Date: fi.ModTime(),
		Modified: fi.ModTime(), Write: true, Extensions: map[string]any{"mounted": false}}
}

// ── Kopier-/Verschiebe-Aufgaben mit Fortschritt (Ereignis casaos:file:operate) ──
type fileTask struct {
	ID            string           `json:"id"`
	Type          string           `json:"type"`
	Item          []map[string]any `json:"item"`
	TotalSize     int64            `json:"total_size"`
	ProcessedSize int64            `json:"processed_size"`
	To            string           `json:"to"`
	Style         string           `json:"style"`
	Finished      bool             `json:"finished"`
}

var tasks = struct {
	sync.Mutex
	m map[string]*fileTask
}{m: map[string]*fileTask{}}

func emitTasks() {
	tasks.Lock()
	list := []fileTask{}
	for _, t := range tasks.m {
		list = append(list, *t)
	}
	tasks.Unlock()
	mbEmit("casaos:file:operate", map[string]any{"file_operate": map[string]any{"data": list}})
}

func treeSize(p string) int64 {
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

type progressWriter struct {
	w io.Writer
	t *fileTask
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	tasks.Lock()
	p.t.ProcessedSize += int64(n)
	tasks.Unlock()
	return n, err
}

func copyTree(src, dst string, t *fileTask) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(&progressWriter{out, t}, in)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		_ = os.Chtimes(target, info.ModTime(), info.ModTime())
		return err
	})
}

// uniqueTarget: bei Namenskonflikt "name (1).ext" usw. (style "skip" überspringt, "overwrite" ersetzt)
func uniqueTarget(p string) string {
	if _, err := os.Lstat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Lstat(c); err != nil {
			return c
		}
	}
}

func runTask(t *fileTask) {
	defer func() {
		tasks.Lock()
		t.Finished = true
		tasks.Unlock()
		emitTasks()
		time.AfterFunc(30*time.Second, func() { tasks.Lock(); delete(tasks.m, t.ID); tasks.Unlock() })
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-tick.C:
				emitTasks()
			case <-done:
				return
			}
		}
	}()
	defer close(done)
	for _, it := range t.Item {
		from, _ := it["from"].(string)
		target := filepath.Join(t.To, filepath.Base(from))
		if _, err := os.Lstat(target); err == nil {
			switch t.Style {
			case "skip":
				continue
			case "overwrite":
				_ = os.RemoveAll(target)
			default:
				target = uniqueTarget(target)
			}
		}
		if t.Type == "move" && os.Rename(from, target) == nil {
			continue
		}
		if err := copyTree(from, target, t); err == nil && t.Type == "move" {
			_ = os.RemoveAll(from)
		}
	}
}

func registerFiles(mux *http.ServeMux, s *storeT) {
	// Ordnerinhalt (Index/Größe wie bei CasaOS; ohne Angabe: alles)
	mux.HandleFunc("GET /v1/folder", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		q := r.URL.Query()
		if p := q.Get("path"); p == "" || p == "/" || p == "/DATA" { // /DATA = CasaOS-Startordner, hier: Übersicht der Volumes
			items := []fsItem{}
			for _, root := range fileRoots {
				if fi, err := os.Stat(root); err == nil {
					items = append(items, itemOf(root, fi))
				}
			}
			ok(w, map[string]any{"content": items, "total": len(items), "index": 1, "size": len(items)})
			return
		}
		p, err := safePath(q.Get("path"))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			fail(w, 200, 500, err.Error())
			return
		}
		items := []fsItem{}
		for _, e := range ents {
			if e.Name() == ".temp" || e.Name() == "@eaDir" || e.Name() == "#recycle" {
				continue
			}
			if fi, err := e.Info(); err == nil {
				items = append(items, itemOf(filepath.Join(p, e.Name()), fi))
			}
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].IsDir != items[j].IsDir {
				return items[i].IsDir
			}
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
		index, _ := strconv.Atoi(q.Get("index"))
		size, _ := strconv.Atoi(q.Get("size"))
		total := len(items)
		if index > 0 && size > 0 {
			a := (index - 1) * size
			if a > total {
				a = total
			}
			b := a + size
			if b > total {
				b = total
			}
			items = items[a:b]
		}
		ok(w, map[string]any{"content": items, "total": total, "index": index, "size": size})
	}))

	mux.HandleFunc("POST /v1/folder", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var in map[string]string
		_ = readJSON(r, &in)
		p, err := safePath(in["path"])
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		if err := os.MkdirAll(p, 0o775); err != nil {
			fail(w, 200, 500, err.Error())
			return
		}
		ok(w, p)
	}))

	rename := s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var in map[string]string
		_ = readJSON(r, &in)
		a, err1 := safePath(in["old_path"])
		b, err2 := safePath(in["new_path"])
		if err1 != nil || err2 != nil || isRoot(a) {
			fail(w, 400, 400, "Pfad nicht erlaubt")
			return
		}
		if _, err := os.Lstat(b); err == nil {
			fail(w, 200, 10002, "Name existiert bereits")
			return
		}
		if err := os.Rename(a, b); err != nil {
			fail(w, 200, 500, err.Error())
			return
		}
		ok(w, b)
	})
	mux.HandleFunc("PUT /v1/folder/name", rename)
	mux.HandleFunc("PUT /v1/file/name", rename)

	mux.HandleFunc("GET /v1/folder/size", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		p, err := safePath(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		ok(w, treeSize(p))
	}))
	mux.HandleFunc("GET /v1/folder/count", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		p, err := safePath(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		ents, _ := os.ReadDir(p)
		ok(w, len(ents))
	}))

	// Neue leere Datei / Inhalt lesen / Inhalt speichern (Text-Editor der Oberfläche)
	mux.HandleFunc("POST /v1/file", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var in map[string]string
		_ = readJSON(r, &in)
		p, err := safePath(in["path"])
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o664)
		if err != nil {
			fail(w, 200, 10002, err.Error())
			return
		}
		f.Close()
		ok(w, p)
	}))
	mux.HandleFunc("GET /v1/file/content", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		p, err := safePath(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		if fi, err := os.Stat(p); err != nil || fi.Size() > 5<<20 {
			fail(w, 200, 500, "Datei zu groß oder nicht lesbar")
			return
		}
		b, _ := os.ReadFile(p)
		ok(w, string(b))
	}))
	mux.HandleFunc("PUT /v1/file", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var in map[string]string
		_ = readJSON(r, &in)
		p, err := safePath(in["path"])
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		if err := os.WriteFile(p, []byte(in["content"]), 0o664); err != nil {
			fail(w, 200, 500, err.Error())
			return
		}
		ok(w, p)
	}))

	// Einzelne Datei herunterladen/anzeigen (Token als ?token=, da Links im Browser geöffnet werden)
	mux.HandleFunc("GET /v1/file", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		p, err := safePath(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		f, err := os.Open(p)
		if err != nil {
			fail(w, 404, 404, err.Error())
			return
		}
		defer f.Close()
		fi, _ := f.Stat()
		if fi.IsDir() {
			fail(w, 400, 400, "Ordner")
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename*=utf-8''"+url.PathEscape(fi.Name()))
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	}))
	// Vorschau (Bilder, Videos, PDFs inline)
	mux.HandleFunc("GET /v1/image", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		p, err := safePath(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeFile(w, r, p)
	}))

	// Mehrere Dateien/Ordner als ZIP (format=zip, files=Pfad1,Pfad2…)
	mux.HandleFunc("GET /v1/batch", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var paths []string
		for _, f := range strings.Split(r.URL.Query().Get("files"), ",") {
			if p, err := safePath(f); err == nil {
				paths = append(paths, p)
			}
		}
		if len(paths) == 0 {
			fail(w, 400, 400, "keine Dateien")
			return
		}
		if len(paths) == 1 {
			if fi, err := os.Stat(paths[0]); err == nil && !fi.IsDir() {
				w.Header().Set("Content-Disposition", "attachment; filename*=utf-8''"+url.PathEscape(fi.Name()))
				http.ServeFile(w, r, paths[0])
				return
			}
		}
		name := "download.zip"
		if len(paths) == 1 {
			name = filepath.Base(paths[0]) + ".zip"
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename*=utf-8''"+url.PathEscape(name))
		zw := zip.NewWriter(w)
		for _, base := range paths {
			parent := filepath.Dir(base)
			_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(parent, p)
				fw, err := zw.Create(rel)
				if err != nil {
					return err
				}
				f, err := os.Open(p)
				if err != nil {
					return nil
				}
				_, _ = io.Copy(fw, f)
				f.Close()
				return nil
			})
		}
		_ = zw.Close()
	}))

	// Löschen: Body ist eine Liste von Pfaden
	mux.HandleFunc("DELETE /v1/batch", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var paths []string
		_ = readJSON(r, &paths)
		for _, raw := range paths {
			p, err := safePath(raw)
			if err != nil || isRoot(p) {
				fail(w, 400, 400, "Pfad nicht erlaubt: "+raw)
				return
			}
			if err := os.RemoveAll(p); err != nil {
				fail(w, 200, 500, err.Error())
				return
			}
		}
		ok(w, paths)
	}))

	// Kopieren/Verschieben im Hintergrund
	mux.HandleFunc("POST /v1/batch/task", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		var t fileTask
		if err := readJSON(r, &t); err != nil {
			fail(w, 400, 400, "ungültig")
			return
		}
		to, err := safePath(t.To)
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		t.To = to
		for _, it := range t.Item {
			from, _ := it["from"].(string)
			p, err := safePath(from)
			if err != nil || isRoot(p) || to == p || strings.HasPrefix(to, p+"/") {
				fail(w, 400, 400, "Quelle nicht erlaubt: "+from)
				return
			}
			it["from"] = p
			t.TotalSize += treeSize(p)
		}
		id := make([]byte, 8)
		_, _ = rand.Read(id)
		t.ID = hex.EncodeToString(id)
		tasks.Lock()
		tasks.m[t.ID] = &t
		tasks.Unlock()
		go runTask(&t)
		ok(w, t.ID)
	}))
	mux.HandleFunc("DELETE /v1/batch/{id}/task", s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		ok(w, r.PathValue("id")) // laufende Kopien werden nicht abgebrochen (shortcut: kein Abbruch, bei Bedarf Kontext einbauen)
	}))

	// Hochladen in Stücken (vue-simple-uploader): GET prüft vorhandene Stücke, POST speichert eines
	chunkDir := func(path, ident string, total int) string {
		h := strings.Map(func(r rune) rune {
			if r == '/' || r == '\\' || r == '.' {
				return '_'
			}
			return r
		}, ident)
		return filepath.Join(path, ".temp", h+strconv.Itoa(total))
	}
	uploadGet := s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		q := r.URL.Query()
		p, err := safePath(q.Get("path"))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		total, _ := strconv.Atoi(q.Get("totalChunks"))
		if _, err := os.Stat(filepath.Join(chunkDir(p, q.Get("identifier"), total), q.Get("chunkNumber"))); err == nil {
			ok(w, "vorhanden")
			return
		}
		w.WriteHeader(http.StatusNoContent) // Stück fehlt -> Client lädt es hoch
	})
	uploadPost := s.auth(func(w http.ResponseWriter, r *http.Request, u *user) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		p, err := safePath(r.FormValue("path"))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		rel := r.FormValue("relativePath")
		if rel == "" {
			rel = r.FormValue("filename")
		}
		dst, err := safePath(filepath.Join(p, filepath.Clean("/"+rel)))
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		total, _ := strconv.Atoi(r.FormValue("totalChunks"))
		chunk := r.FormValue("chunkNumber")
		f, _, err := r.FormFile("file")
		if err != nil {
			fail(w, 400, 400, err.Error())
			return
		}
		defer f.Close()
		_ = os.MkdirAll(filepath.Dir(dst), 0o775)
		if total <= 1 {
			out, err := os.Create(dst)
			if err != nil {
				fail(w, 200, 500, err.Error())
				return
			}
			_, err = io.Copy(out, f)
			out.Close()
			if err != nil {
				fail(w, 200, 500, err.Error())
				return
			}
			ok(w, "ok")
			return
		}
		dir := chunkDir(p, r.FormValue("identifier"), total)
		_ = os.MkdirAll(dir, 0o700)
		out, err := os.Create(filepath.Join(dir, filepath.Base(chunk)))
		if err != nil {
			fail(w, 200, 500, err.Error())
			return
		}
		_, err = io.Copy(out, f)
		out.Close()
		if err != nil {
			fail(w, 200, 500, err.Error())
			return
		}
		ents, _ := os.ReadDir(dir)
		if len(ents) < total {
			ok(w, "stück")
			return
		}
		// alle Stücke da -> zusammensetzen
		outF, err := os.Create(dst)
		if err != nil {
			fail(w, 200, 500, err.Error())
			return
		}
		for i := 1; i <= total; i++ {
			c, err := os.Open(filepath.Join(dir, strconv.Itoa(i)))
			if err != nil {
				outF.Close()
				fail(w, 200, 500, "Stück fehlt: "+strconv.Itoa(i))
				return
			}
			_, _ = io.Copy(outF, c)
			c.Close()
		}
		outF.Close()
		_ = os.RemoveAll(dir)
		_ = os.Remove(filepath.Join(p, ".temp")) // nur wenn leer
		ok(w, "fertig")
	})
	mux.HandleFunc("GET /v2/casaos/file/upload", uploadGet)
	mux.HandleFunc("POST /v2/casaos/file/upload", uploadPost)
	mux.HandleFunc("GET /v1/file/upload", uploadGet)
	mux.HandleFunc("POST /v1/file/upload", uploadPost)

	_ = json.Valid
}
