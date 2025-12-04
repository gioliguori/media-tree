package domain

// NodeType rappresenta il tipo di nodo
type NodeType string

const (
	NodeTypeInjection NodeType = "injection"
	NodeTypeRelay     NodeType = "relay"
	NodeTypeEgress    NodeType = "egress"
)

// NodeSpec è la specifica per creare un nodo
// Usata dal controller per richiedere provisioning
type NodeSpec struct {
	NodeId   string
	NodeType NodeType
	TreeId   string
	Layer    int
}

// NodeInfo è quello che ritorna il provisioner dopo aver creato un nodo
// Contiene tutte le info per connettere il nodo
type NodeInfo struct {
	NodeId   string
	NodeType NodeType
	TreeId   string
	Layer    int

	// Container info
	ContainerId string

	// Network - INTERNAL (comunicazione inter-container)
	InternalHost     string // es: "injection-1"
	InternalAPIPort  int    // sempre 7070
	InternalRTPAudio int    // injection: 5000, relay/egress: 5002
	InternalRTPVideo int    // injection: 5002, relay/egress: 5004

	// Network - EXTERNAL (controller accede da host)
	ExternalHost    string // es: "localhost"
	ExternalAPIPort int    // es: 7070, 7071, 7072... (mappato)

	// Janus (solo per injection/egress)
	JanusContainerId string
	JanusHost        string // es: "injection-1-janus-vr"
	JanusWSPort      int    // internal: sempre 8188
	JanusHTTPPort    int    // internal: sempre 8088

	// WebRTC Range (solo injection/egress)
	WebRTCPortStart int // es: 20000
	WebRTCPortEnd   int
}

// IsInjection verifica se è un injection node
func (n *NodeInfo) IsInjection() bool {
	return n.NodeType == NodeTypeInjection
}

// IsRelay verifica se è un relay node
func (n *NodeInfo) IsRelay() bool {
	return n.NodeType == NodeTypeRelay
}

// IsEgress verifica se è un egress node
func (n *NodeInfo) IsEgress() bool {
	return n.NodeType == NodeTypeEgress
}

// NeedsJanus verifica se il nodo richiede Janus
func (n *NodeInfo) NeedsJanus() bool {
	return n.IsInjection() || n.IsEgress()
}

// GetInternalAPIURL ritorna URL interno per API
func (n *NodeInfo) GetInternalAPIURL() string {
	return "http://" + n.InternalHost + ":" + string(rune(n.InternalAPIPort))
}

// GetExternalAPIURL ritorna URL esterno per API
func (n *NodeInfo) GetExternalAPIURL() string {
	return "http://" + n.ExternalHost + ":" + string(rune(n.ExternalAPIPort))
}
