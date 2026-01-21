package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Client represents a connected WebSocket client
type Client struct {
	ID     string
	Conn   *websocket.Conn
	Room   *Room
	Color  string
	Send   chan []byte
	closed bool
	mu     sync.Mutex
}

// Room represents a collaborative drawing room
type Room struct {
	ID       string
	Clients  map[*Client]bool
	Elements []DrawElement
	mu       sync.RWMutex
}

// Hub manages all rooms and clients
type Hub struct {
	Rooms map[string]*Room
	mu    sync.RWMutex
}

var hub = &Hub{
	Rooms: make(map[string]*Room),
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// User colors for visual distinction
var userColors = []string{
	"#e74c3c", "#3498db", "#2ecc71", "#f39c12",
	"#9b59b6", "#1abc9c", "#e67e22", "#34495e",
}

var colorIndex = 0
var colorMu sync.Mutex

func getNextColor() string {
	colorMu.Lock()
	defer colorMu.Unlock()
	color := userColors[colorIndex%len(userColors)]
	colorIndex++
	return color
}

// GetOrCreateRoom returns an existing room or creates a new one
func (h *Hub) GetOrCreateRoom(roomID string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	if room, exists := h.Rooms[roomID]; exists {
		return room
	}

	room := &Room{
		ID:       roomID,
		Clients:  make(map[*Client]bool),
		Elements: make([]DrawElement, 0),
	}
	h.Rooms[roomID] = room
	return room
}

// AddClient adds a client to the room
func (r *Room) AddClient(client *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Clients[client] = true
}

// RemoveClient removes a client from the room
func (r *Room) RemoveClient(client *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.Clients, client)
}

// Broadcast sends a message to all clients in the room except the sender
func (r *Room) Broadcast(msg []byte, sender *Client) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for client := range r.Clients {
		if client != sender {
			client.mu.Lock()
			if !client.closed {
				select {
				case client.Send <- msg:
				default:
					// Channel full, skip this message
				}
			}
			client.mu.Unlock()
		}
	}
}

// BroadcastToAll sends a message to all clients including sender
func (r *Room) BroadcastToAll(msg []byte) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for client := range r.Clients {
		client.mu.Lock()
		if !client.closed {
			select {
			case client.Send <- msg:
			default:
			}
		}
		client.mu.Unlock()
	}
}

// AddElement adds a drawing element to the room's state
func (r *Room) AddElement(element DrawElement) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Elements = append(r.Elements, element)
}

// ClearElements clears all elements from the room
func (r *Room) ClearElements() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Elements = make([]DrawElement, 0)
}

// GetElements returns a copy of all elements
func (r *Room) GetElements() []DrawElement {
	r.mu.RLock()
	defer r.mu.RUnlock()
	elements := make([]DrawElement, len(r.Elements))
	copy(elements, r.Elements)
	return elements
}

// GetUserList returns list of users in the room
func (r *Room) GetUserList() []UserInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	users := make([]UserInfo, 0, len(r.Clients))
	for client := range r.Clients {
		users = append(users, UserInfo{
			ID:    client.ID,
			Color: client.Color,
		})
	}
	return users
}

// writePump pumps messages from the Send channel to the WebSocket connection
func (c *Client) writePump() {
	defer func() {
		c.Conn.Close()
	}()

	for msg := range c.Send {
		if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// readPump pumps messages from the WebSocket connection to the room
func (c *Client) readPump() {
	defer func() {
		c.mu.Lock()
		c.closed = true
		close(c.Send)
		c.mu.Unlock()

		c.Room.RemoveClient(c)
		c.Conn.Close()

		// Notify others that user left
		leaveMsg := Message{
			Type:   TypeLeave,
			UserID: c.ID,
		}
		if msgBytes, err := json.Marshal(leaveMsg); err == nil {
			c.Room.Broadcast(msgBytes, c)
		}

		// Send updated user list
		userListMsg := Message{
			Type: TypeUserList,
			Data: c.Room.GetUserList(),
		}
		if msgBytes, err := json.Marshal(userListMsg); err == nil {
			c.Room.BroadcastToAll(msgBytes)
		}

		log.Printf("Client %s left room %s", c.ID, c.Room.ID)
	}()

	for {
		_, msgBytes, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			log.Printf("JSON unmarshal error: %v", err)
			continue
		}

		msg.UserID = c.ID

		switch msg.Type {
		case TypeDraw:
			// Parse and store the draw element
			if dataBytes, err := json.Marshal(msg.Data); err == nil {
				var element DrawElement
				if err := json.Unmarshal(dataBytes, &element); err == nil {
					element.UserID = c.ID
					c.Room.AddElement(element)
					msg.Data = element
				}
			}
			// Broadcast to others
			if broadcastBytes, err := json.Marshal(msg); err == nil {
				c.Room.Broadcast(broadcastBytes, c)
			}

		case TypeCursor:
			// Broadcast cursor position to others
			if broadcastBytes, err := json.Marshal(msg); err == nil {
				c.Room.Broadcast(broadcastBytes, c)
			}

		case TypeClear:
			c.Room.ClearElements()
			// Broadcast clear to all
			if broadcastBytes, err := json.Marshal(msg); err == nil {
				c.Room.BroadcastToAll(broadcastBytes)
			}
		}
	}
}

func handleWebSocket(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := c.Query("userId")

	if roomID == "" {
		roomID = "default"
	}
	if userID == "" {
		userID = generateID()
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	room := hub.GetOrCreateRoom(roomID)
	color := getNextColor()

	client := &Client{
		ID:    userID,
		Conn:  conn,
		Room:  room,
		Color: color,
		Send:  make(chan []byte, 256),
	}

	room.AddClient(client)
	log.Printf("Client %s joined room %s", userID, roomID)

	// Send sync message with current canvas state
	syncMsg := Message{
		Type:   TypeSync,
		UserID: userID,
		Data: map[string]interface{}{
			"elements": room.GetElements(),
			"userId":   userID,
			"color":    color,
		},
	}
	if syncBytes, err := json.Marshal(syncMsg); err == nil {
		conn.WriteMessage(websocket.TextMessage, syncBytes)
	}

	// Notify others about new user
	joinMsg := Message{
		Type:   TypeJoin,
		UserID: userID,
		Data: UserInfo{
			ID:    userID,
			Color: color,
		},
	}
	if joinBytes, err := json.Marshal(joinMsg); err == nil {
		room.Broadcast(joinBytes, client)
	}

	// Send user list to all
	userListMsg := Message{
		Type: TypeUserList,
		Data: room.GetUserList(),
	}
	if userListBytes, err := json.Marshal(userListMsg); err == nil {
		room.BroadcastToAll(userListBytes)
	}

	// Start pumps
	go client.writePump()
	client.readPump()
}

// generateID creates a simple ID for room code
// TODO: Can probably fix this to be more robust. Works ok for now, but edge case exist.
func generateID() string {
	return randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[randomInt(len(letters))]
	}
	return string(b)
}

var randMu sync.Mutex
var randSeed uint64 = 1

func randomInt(max int) int {
	randMu.Lock()
	defer randMu.Unlock()
	randSeed = randSeed*1103515245 + 12345
	return int(randSeed/65536) % max
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Serve static files
	r.Static("/static", "./static")

	// Serve index.html at root
	r.GET("/", func(c *gin.Context) {
		c.File("./static/index.html")
	})

	// Room-specific routes
	r.GET("/room/:roomId", func(c *gin.Context) {
		c.File("./static/index.html")
	})

	// WebSocket endpoint
	r.GET("/ws/:roomId", handleWebSocket)

	log.Println("Server starting on :8080")
	log.Println("Open http://localhost:8080 in your browser")
	if err := r.Run(":8080"); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
