package domain

import "fmt"

type NodeType string

const (
	NodeTypeInjection NodeType = "injection"
	NodeTypeRelay     NodeType = "relay"
	NodeTypeEgress    NodeType = "egress"
)

// NodeSpec è la specifica per creare un nodo
// Usata dal controller per richiedere provisioning
type NodeSpec struct {
	NodeId   string   `json:"nodeId"`
	NodeType NodeType `json:"nodeType"`
	MaxSlots int      `json:"maxSlots"`
}

// NodeInfo è quello che ritorna il provisioner dopo aver creato un nodo
// Contiene tutte le info per connettere il nodo
type NodeInfo struct {
	NodeId   string   `json:"nodeId"`
	NodeType NodeType `json:"nodeType"`
	Role     string   `json:"role"`
	MaxSlots int      `json:"maxSlots" redis:"maxSlots"`
	// Container info
	ContainerId string `json:"containerId"`

	// Network - INTERNAL
	// Usati per comunicazione tra nodi
	InternalHost     string `json:"internalHost"`
	InternalAPIPort  int    `json:"internalApiPort"`
	InternalRTPAudio int    `json:"internalRtpAudio"`
	InternalRTPVideo int    `json:"internalRtpVideo"`

	// Network - EXTERNAL
	// Usati dal controller
	ExternalHost    string `json:"externalHost"`
	ExternalAPIPort int    `json:"externalApiPort"`

	// Janus (solo per injection/egress)
	JanusContainerId string `json:"janusContainerId,omitempty"`
	JanusHost        string `json:"janusHost,omitempty"`
	JanusWSPort      int    `json:"janusWsPort,omitempty"`
	JanusHTTPPort    int    `json:"janusHttpPort,omitempty"`

	// WebRTC Range (solo injection/egress)
	WebRTCPortStart int `json:"webrtcPortStart,omitempty"`
	WebRTCPortEnd   int `json:"webrtcPortEnd,omitempty"`

	StreamPortStart int `json:"streamPortStart,omitempty" redis:"streamPortStart"`
	StreamPortEnd   int `json:"streamPortEnd,omitempty" redis:"streamPortEnd"`
}

// Helper methods per controlli veloci

func (n *NodeInfo) IsInjection() bool {
	return n.NodeType == NodeTypeInjection
}

func (n *NodeInfo) IsRelay() bool {
	return n.NodeType == NodeTypeRelay
}

func (n *NodeInfo) IsEgress() bool {
	return n.NodeType == NodeTypeEgress
}

func (n *NodeInfo) NeedsJanus() bool {
	return n.IsInjection() || n.IsEgress()
}

// GetInternalAPIURL ritorna URL interno per API
func (n *NodeInfo) GetInternalAPIURL() string {
	return fmt.Sprintf("http://%s:%d", n.InternalHost, n.InternalAPIPort)
}

// GetExternalAPIURL ritorna URL esterno per API
func (n *NodeInfo) GetExternalAPIURL() string {
	return fmt.Sprintf("http://%s:%d", n.ExternalHost, n.ExternalAPIPort)
}
