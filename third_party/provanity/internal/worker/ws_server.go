package worker

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	wsNonceSize    = 12
	wsWriteTimeout = 5 * time.Second
)

type wsServer struct {
	listener net.Listener
	server   *http.Server
	key      []byte
	onStop   context.CancelFunc

	mu      sync.Mutex
	clients map[*wsClient]struct{}
	closed  bool
}

type wsClient struct {
	server  *wsServer
	conn    *websocket.Conn
	writeMu sync.Mutex
	once    sync.Once
}

func startWSServer(port int, key []byte, onStop context.CancelFunc) (*wsServer, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("WebSocket server requires an encryption key")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return nil, fmt.Errorf("start WebSocket server: %w", err)
	}

	ws := &wsServer{
		listener: listener,
		key:      append([]byte(nil), key...),
		onStop:   onStop,
		clients:  make(map[*wsClient]struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.accept)
	ws.server = &http.Server{Handler: mux}

	go func() {
		err := ws.server.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			ws.Close()
		}
	}()

	return ws, nil
}

func (s *wsServer) Broadcast(event map[string]any) {
	clients := s.snapshotClients()
	for _, client := range clients {
		if err := client.sendEvent(event); err != nil {
			client.close(websocket.StatusInternalError, "write failed")
		}
	}
}

func (s *wsServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	clients := make([]*wsClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = s.server.Shutdown(ctx)
	cancel()

	for _, client := range clients {
		client.close(websocket.StatusNormalClosure, "worker stopped")
	}
}

func (s *wsServer) accept(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(64 * 1024)

	client := &wsClient{server: s, conn: conn}
	if !s.addClient(client) {
		_ = conn.Close(websocket.StatusGoingAway, "server closing")
		return
	}
	if err := client.sendEvent(baseEvent("subscribed")); err != nil {
		client.close(websocket.StatusInternalError, "subscribe failed")
		return
	}
	go client.readLoop()
}

func (s *wsServer) addClient(client *wsClient) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.clients[client] = struct{}{}
	return true
}

func (s *wsServer) removeClient(client *wsClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, client)
}

func (s *wsServer) snapshotClients() []*wsClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	clients := make([]*wsClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	return clients
}

func (c *wsClient) readLoop() {
	defer c.close(websocket.StatusNormalClosure, "client closed")
	for {
		messageType, data, err := c.conn.Read(context.Background())
		if err != nil {
			return
		}
		plain, err := decodeWSFrame(c.server.key, messageType, data)
		if err != nil {
			c.close(websocket.StatusPolicyViolation, "decrypt failed")
			return
		}
		c.handleCommand(plain)
	}
}

func (c *wsClient) handleCommand(data []byte) {
	var command struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(data, &command); err != nil {
		_ = c.sendEvent(errorEvent("invalid command JSON"))
		return
	}

	switch command.Command {
	case "stop":
		if c.server.onStop != nil {
			c.server.onStop()
		}
	case "ping":
		_ = c.sendEvent(baseEvent("pong"))
	case "":
		_ = c.sendEvent(errorEvent("unknown command: "))
	default:
		_ = c.sendEvent(errorEvent("unknown command: " + command.Command))
	}
}

func (c *wsClient) sendEvent(event map[string]any) error {
	messageType, data, err := encodeWSFrame(c.server.key, event)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), wsWriteTimeout)
	defer cancel()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(ctx, messageType, data)
}

func (c *wsClient) close(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		c.server.removeClient(c)
		_ = c.conn.CloseNow()
	})
}

func encodeWSFrame(key []byte, event map[string]any) (websocket.MessageType, []byte, error) {
	plain, err := json.Marshal(event)
	if err != nil {
		return websocket.MessageText, nil, err
	}
	if len(key) == 0 {
		return websocket.MessageText, plain, nil
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return websocket.MessageBinary, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return websocket.MessageBinary, nil, err
	}
	nonce := make([]byte, wsNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return websocket.MessageBinary, nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plain, nil)
	frame := make([]byte, 0, len(nonce)+len(ciphertext))
	frame = append(frame, nonce...)
	frame = append(frame, ciphertext...)
	return websocket.MessageBinary, frame, nil
}

func decodeWSFrame(key []byte, messageType websocket.MessageType, data []byte) ([]byte, error) {
	if len(key) == 0 {
		if messageType != websocket.MessageText {
			return nil, fmt.Errorf("plaintext WebSocket frames must be text")
		}
		return data, nil
	}
	if messageType != websocket.MessageBinary {
		return nil, fmt.Errorf("encrypted WebSocket frames must be binary")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < wsNonceSize+gcm.Overhead() {
		return nil, fmt.Errorf("encrypted WebSocket frame is too short")
	}
	nonce := data[:wsNonceSize]
	ciphertext := data[wsNonceSize:]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return plain, nil
}
