package session

import "time"

// CreateSessionRequest input per CreateSession
type CreateSessionRequest struct {
	SessionId string `json:"sessionId" binding:"required"`
}

// SessionInfo response CreateSession
type SessionInfo struct {
	SessionId       string    `json:"sessionId"`
	InjectionNodeId string    `json:"injectionNodeId"`
	AudioSsrc       int       `json:"audioSsrc"`
	VideoSsrc       int       `json:"videoSsrc"`
	RoomId          int       `json:"roomId"`
	WhipEndpoint    string    `json:"whipEndpoint"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"createdAt"`
}

// ViewSessionRequest input per ProvisionViewer
type ViewSessionRequest struct {
	SessionId string `json:"sessionId"`
}

// ViewSessionResponse output ProvisionViewer
type ViewSessionResponse struct {
	SessionId    string   `json:"sessionId"`
	EgressNodeId string   `json:"egressNodeId"`
	EgressPort   int      `json:"egressPort"`
	WhepEndpoint string   `json:"whepEndpoint"`
	Path         []string `json:"path"`
	ViewerCount  int      `json:"viewerCount"`
	Reused       bool     `json:"reused"`
}

// SessionSummary per lista sessioni
type SessionSummary struct {
	SessionId       string    `json:"sessionId"`
	InjectionNodeId string    `json:"injectionNodeId"`
	ViewerCount     int       `json:"viewerCount"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Injection Node

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

// Recipient RTP target (usato solo nella risposta dell'Injection)
type Recipient struct {
	Host      string `json:"host"`
	AudioPort int    `json:"audioPort"`
	VideoPort int    `json:"videoPort"`
}

// PathInfo informazioni singolo path
type PathInfo struct {
	EgressNodeId string   `json:"egressNodeId"`
	Hops         []string `json:"hops"`
	WhepEndpoint string   `json:"whepEndpoint"`
}
