package session

import "time"

type SessionScale string

const (
	ScaleLow    SessionScale = "low"    // 1 egress (broadcaster piccolo)
	ScaleMedium SessionScale = "medium" // 2 egress (broadcaster medio)
	ScaleHigh   SessionScale = "high"   // 3 egress (broadcaster grande)
)

func ScaleToEgressCount(scale SessionScale) int {
	switch scale {
	case ScaleLow:
		return 1
	case ScaleMedium:
		return 2
	case ScaleHigh:
		return 3
	default:
		return 1
	}
}

// CreateSessionRequest input per CreateSession
type CreateSessionRequest struct {
	SessionId       string       `json:"session_id" binding:"required"`
	TreeId          string       `json:"tree_id"`
	InjectionNodeId string       `json:"injection_node_id"`
	EgressNodeIds   []string     `json:"egress_node_ids"`
	Scale           SessionScale `json:"scale,omitempty"` // "low", "medium", "high"
}

func (r *CreateSessionRequest) IsManual() bool {
	return r.TreeId != ""
}

// SessionInfo response
type SessionInfo struct {
	SessionId       string     `json:"session_id"`
	TreeId          string     `json:"tree_id"`
	InjectionNodeId string     `json:"injection_node_id"`
	AudioSsrc       int        `json:"audio_ssrc"`
	VideoSsrc       int        `json:"video_ssrc"`
	RoomId          int        `json:"room_id"`
	WhipEndpoint    string     `json:"whip_endpoint"`
	Paths           []PathInfo `json:"paths"`
	EgressCount     int        `json:"egress_count"`
	Active          bool       `json:"active"`
	CreatedAt       time.Time  `json:"created_at"`
}

// PathInfo informazioni singolo path
type PathInfo struct {
	EgressNodeId string   `json:"egress_node_id"`
	Hops         []string `json:"hops"`
	WhepEndpoint string   `json:"whep_endpoint"`
}

// Recipient
type Recipient struct {
	Host      string `json:"host"`
	AudioPort int    `json:"audioPort"`
	VideoPort int    `json:"videoPort"`
}

// InjectionSessionRequest per chiamata HTTP a injection node
type InjectionSessionRequest struct {
	SessionId string `json:"sessionId"`
	RoomId    int    `json:"roomId"`
	AudioSsrc int    `json:"audioSsrc"`
	VideoSsrc int    `json:"videoSsrc"`
}

// InjectionSessionResponse risposta da injection node
type InjectionSessionResponse struct {
	SessionId  string      `json:"sessionId"`
	Endpoint   string      `json:"endpoint"`
	RoomId     int         `json:"roomId"`
	AudioSsrc  int         `json:"audioSsrc"`
	VideoSsrc  int         `json:"videoSsrc"`
	Recipients []Recipient `json:"recipients"`
}

// RelaySessionEvent evento pub/sub per relay
type RelaySessionEvent struct {
	Type      string  `json:"type"` // "session-created"
	SessionId string  `json:"sessionId"`
	TreeId    string  `json:"treeId"`
	AudioSsrc int     `json:"audioSsrc"`
	VideoSsrc int     `json:"videoSsrc"`
	Routes    []Route `json:"routes"`
}

// Route singola route per relay
type Route struct {
	TargetId  string `json:"targetId"`
	Host      string `json:"host"`
	AudioPort int    `json:"audioPort"`
	VideoPort int    `json:"videoPort"`
}

// EgressSessionEvent evento pub/sub per egress
type EgressSessionEvent struct {
	Type      string `json:"type"` // "session-created"
	SessionId string `json:"sessionId"`
	TreeId    string `json:"treeId"`
	AudioSsrc int    `json:"audioSsrc"`
	VideoSsrc int    `json:"videoSsrc"`
}
