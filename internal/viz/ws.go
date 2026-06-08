package viz

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type WebSocketEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

type WebSocketManager struct {
	mu      sync.Mutex
	clients map[*webSocketClient]bool
}

type webSocketClient struct {
	conn net.Conn
	send chan []byte
	done chan struct{}
	once sync.Once
}

func NewWebSocketManager() *WebSocketManager {
	return &WebSocketManager{clients: make(map[*webSocketClient]bool)}
}

func (m *WebSocketManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return
	}
	accept := websocketAccept(key)
	if _, err := fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		conn.Close()
		return
	}
	if err := buf.Flush(); err != nil {
		conn.Close()
		return
	}
	client := &webSocketClient{conn: conn, send: make(chan []byte, 32), done: make(chan struct{})}
	m.add(client)
	go client.writeLoop(m)
	go client.readLoop(m)
	m.Send(client, WebSocketEvent{Type: "connected"})
}

func (m *WebSocketManager) Broadcast(event WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	m.mu.Lock()
	clients := make([]*webSocketClient, 0, len(m.clients))
	for client := range m.clients {
		clients = append(clients, client)
	}
	m.mu.Unlock()
	for _, client := range clients {
		select {
		case client.send <- data:
		default:
			m.remove(client)
		}
	}
}

func (m *WebSocketManager) Send(client *webSocketClient, event WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
		m.remove(client)
	}
}

func (m *WebSocketManager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.clients)
}

func (m *WebSocketManager) add(client *webSocketClient) {
	m.mu.Lock()
	m.clients[client] = true
	m.mu.Unlock()
}

func (m *WebSocketManager) remove(client *webSocketClient) {
	client.once.Do(func() {
		m.mu.Lock()
		delete(m.clients, client)
		m.mu.Unlock()
		close(client.done)
		client.conn.Close()
	})
}

func (c *webSocketClient) writeLoop(manager *WebSocketManager) {
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case data, ok := <-c.send:
			if !ok {
				return
			}
			if err := writeWebSocketTextFrame(c.conn, data); err != nil {
				manager.remove(c)
				return
			}
		case <-heartbeat.C:
			if err := writeWebSocketTextFrame(c.conn, []byte(`{"type":"ping"}`)); err != nil {
				manager.remove(c)
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *webSocketClient) readLoop(manager *WebSocketManager) {
	_, _ = io.Copy(io.Discard, c.conn)
	manager.remove(c)
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func writeWebSocketTextFrame(conn net.Conn, data []byte) error {
	header := []byte{0x81}
	length := len(data)
	switch {
	case length < 126:
		header = append(header, byte(length))
	case length <= 65535:
		header = append(header, 126, byte(length>>8), byte(length))
	default:
		header = append(header, 127, byte(length>>56), byte(length>>48), byte(length>>40), byte(length>>32), byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	}
	if _, err := conn.Write(header); err != nil {
		return err
	}
	_, err := conn.Write(data)
	return err
}
