package main

// MessageType constants for WebSocket communication
const (
	TypeDraw      = "draw"
	TypeClear     = "clear"
	TypeSync      = "sync"
	TypeJoin      = "join"
	TypeLeave     = "leave"
	TypeCursor    = "cursor"
	TypeUserList  = "userlist"
)

// Message is the envelope for all WebSocket messages
type Message struct {
	Type   string      `json:"type"`
	UserID string      `json:"userId,omitempty"`
	Data   interface{} `json:"data,omitempty"`
}

// DrawElement represents a single drawing element (line stroke)
type DrawElement struct {
	ID          string  `json:"id"`
	Type        string  `json:"elementType"` // "line", "rectangle", "ellipse", etc.
	X1          float64 `json:"x1"`
	Y1          float64 `json:"y1"`
	X2          float64 `json:"x2"`
	Y2          float64 `json:"y2"`
	StrokeColor string  `json:"strokeColor"`
	StrokeWidth float64 `json:"strokeWidth"`
	UserID      string  `json:"userId"`
}

// CursorPosition represents a user's cursor location
type CursorPosition struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	UserID string  `json:"userId"`
}

// SyncData contains the full canvas state for new joiners
type SyncData struct {
	Elements []DrawElement `json:"elements"`
}

// UserInfo represents a connected user
type UserInfo struct {
	ID    string `json:"id"`
	Color string `json:"color"`
}
