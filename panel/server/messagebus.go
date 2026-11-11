package main

import (
	"encoding/hex"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Minimaler Socket.IO-v2-Server (Engine.IO v3, nur WebSocket-Transport) für die Widgets.
// Protokoll: Server "0{handshake}", "40" (verbunden); Client "2" (ping) -> Server "3" (pong);
// Ereignisse: "42[\"name\",payload]".

type mbClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *mbClient) send(msg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, []byte(msg))
}

var mb = struct {
	sync.Mutex
	clients map[*mbClient]bool
}{clients: map[*mbClient]bool{}}

func mbEmit(name string, props map[string]any) {
	// CasaOS-Format: Properties sind JSON-Strings
	p := map[string]string{}
	for k, v := range props {
		if s, ok := v.(string); ok {
			p[k] = s
		} else {
			b, _ := json.Marshal(v)
			p[k] = string(b)
		}
	}
	b, _ := json.Marshal([]any{name, map[string]any{"SourceID": "casaos", "Name": name, "Properties": p, "Timestamp": time.Now().Unix()}})
	msg := "42" + string(b)
	mb.Lock()
	list := make([]*mbClient, 0, len(mb.clients))
	for c := range mb.clients {
		list = append(list, c)
	}
	mb.Unlock()
	for _, c := range list {
		if c.send(msg) != nil {
			c.conn.Close()
		}
	}
}

func registerMessageBus(mux *http.ServeMux, s *storeT) {
	up := websocket.Upgrader{
		ReadBufferSize: 1024, WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool { return true }, // gleiche Seite; keine Cookies im Spiel
	}
	mux.HandleFunc("/v2/message_bus/socket.io/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("transport") != "websocket" {
			// Polling wird nicht angeboten – der Client fällt dann auf WebSocket zurück
			http.Error(w, "nur websocket", http.StatusBadRequest)
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := &mbClient{conn: conn}
		sid := make([]byte, 10)
		_, _ = rand.Read(sid)
		_ = c.send(`0{"sid":"` + hex.EncodeToString(sid) + `","upgrades":[],"pingInterval":25000,"pingTimeout":60000}`)
		_ = c.send("40")
		mb.Lock()
		mb.clients[c] = true
		mb.Unlock()
		// sofort ein erster Messwert, damit die Widgets nicht leer starten
		go mbEmit("casaos:system:utilization", utilProps())
		conn.SetReadLimit(64 << 10)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			_, m, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if string(m) == "2" {
				_ = c.send("3")
			}
		}
		mb.Lock()
		delete(mb.clients, c)
		mb.Unlock()
		conn.Close()
	})

	go func() {
		for {
			time.Sleep(3 * time.Second)
			mb.Lock()
			n := len(mb.clients)
			mb.Unlock()
			if n > 0 { // nur messen, wenn jemand zuschaut
				mbEmit("casaos:system:utilization", utilProps())
			}
		}
	}()
}

func utilProps() map[string]any {
	return map[string]any{"sys_cpu": cpuData(), "sys_mem": memInfo(), "sys_net": netStats(),
		"sys_disk": sysDisk(), "sys_usb": []any{}}
}
