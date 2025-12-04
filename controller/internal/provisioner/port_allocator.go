package provisioner

import (
	"fmt"
	"sync"
)

// PortAllocator gestisce allocazione porte per Docker locale
// Previene conflitti tra container che usano stesse porte interne
type PortAllocator struct {
	mu sync.Mutex

	usedPorts map[int]bool

	// ========================================
	// API NODI (singole porte)
	// Range: 7070-7100 (30 nodi)
	// ========================================
	apiPortMin  int
	apiPortNext int
	apiPortMax  int

	// ========================================
	// JANUS HTTP (singole porte)
	// Range: 8088-8120 (30 istanze)
	// ========================================
	janusHTTPMin  int
	janusHTTPNext int
	janusHTTPMax  int

	// ========================================
	// JANUS WEBSOCKET (singole porte)
	// Range: 8188-8220 (30 istanze)
	// ========================================
	janusWSMin  int
	janusWSNext int
	janusWSMax  int

	// ========================================
	// WEBRTC RANGES (per Janus instances)
	// Range: 20000-25000 (50 istanze × 100 porte)
	// Ogni Janus riceve 100 porte consecutive
	// ========================================
	webrtcRangeStart int // 20000
	webrtcRangeSize  int // 100 porte per istanza
	webrtcRangeNext  int // Prossimo range disponibile
	webrtcRangeMax   int // 25000
}

// NewPortAllocator crea allocatore con range default
func NewPortAllocator() *PortAllocator {
	return &PortAllocator{
		usedPorts: make(map[int]bool),

		// API: 7070-7100 (30 nodi)
		apiPortMin:  7070,
		apiPortNext: 7070,
		apiPortMax:  7100,

		// Janus HTTP: 8088-8120 (30 istanze)
		janusHTTPMin:  8088,
		janusHTTPNext: 8088,
		janusHTTPMax:  8120,

		// Janus WS: 8188-8220 (30 istanze)
		janusWSMin:  8188,
		janusWSNext: 8188,
		janusWSMax:  8220,

		// WebRTC: 20000-25000 (50 istanze × 100 porte)
		webrtcRangeStart: 20000,
		webrtcRangeSize:  100,
		webrtcRangeNext:  20000,
		webrtcRangeMax:   25000,
	}
}

// AllocateAPIPort alloca una porta per API nodo
// Ritorna: port, error
func (pa *PortAllocator) AllocateAPIPort() (int, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	if pa.apiPortNext > pa.apiPortMax {
		return 0, fmt.Errorf("no more API ports available (max: %d)", pa.apiPortMax)
	}

	port := pa.apiPortNext
	pa.usedPorts[port] = true
	pa.apiPortNext++

	return port, nil
}

// AllocateJanusPorts alloca HTTP + WS per una istanza Janus
// Ritorna: httpPort, wsPort, error
func (pa *PortAllocator) AllocateJanusPorts() (httpPort int, wsPort int, err error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	if pa.janusHTTPNext > pa.janusHTTPMax {
		return 0, 0, fmt.Errorf("no more Janus HTTP ports available (max: %d)", pa.janusHTTPMax)
	}
	if pa.janusWSNext > pa.janusWSMax {
		return 0, 0, fmt.Errorf("no more Janus WS ports available (max: %d)", pa.janusWSMax)
	}

	httpPort = pa.janusHTTPNext
	pa.usedPorts[httpPort] = true
	pa.janusHTTPNext++

	wsPort = pa.janusWSNext
	pa.usedPorts[wsPort] = true
	pa.janusWSNext++

	return httpPort, wsPort, nil
}

// AllocateWebRTCRange alloca un range di porte WebRTC per Janus
// Ogni istanza Janus riceve 100 porte consecutive
// Ritorna: startPort, endPort, error
// Esempio: (20000, 20099), (20100, 20199), (20200, 20299)...
func (pa *PortAllocator) AllocateWebRTCRange() (int, int, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	startPort := pa.webrtcRangeNext
	endPort := startPort + pa.webrtcRangeSize - 1

	if endPort > pa.webrtcRangeMax {
		return 0, 0, fmt.Errorf("no more WebRTC ranges available (max: %d)", pa.webrtcRangeMax)
	}

	// Segna tutte le porte nel range come usate
	for port := startPort; port <= endPort; port++ {
		pa.usedPorts[port] = true
	}

	// Avanza al prossimo range (NO overlap)
	pa.webrtcRangeNext = endPort + 1

	return startPort, endPort, nil
}

// Release rilascia una singola porta
func (pa *PortAllocator) Release(port int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	delete(pa.usedPorts, port)
}

// ReleaseRange rilascia un intero range di porte
// Usato per rilasciare WebRTC ranges
func (pa *PortAllocator) ReleaseRange(startPort, endPort int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	for port := startPort; port <= endPort; port++ {
		delete(pa.usedPorts, port)
	}
}

// ReleasePorts rilascia multiple porte
func (pa *PortAllocator) ReleasePorts(ports ...int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	for _, port := range ports {
		delete(pa.usedPorts, port)
	}
}

// IsPortUsed verifica se una porta è allocata
func (pa *PortAllocator) IsPortUsed(port int) bool {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	return pa.usedPorts[port]
}

// GetUsedPorts ritorna lista di tutte le porte allocate
func (pa *PortAllocator) GetUsedPorts() []int {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	ports := make([]int, 0, len(pa.usedPorts))
	for port := range pa.usedPorts {
		ports = append(ports, port)
	}
	return ports
}

// GetAvailableAPIPorts ritorna numero porte API disponibili
func (pa *PortAllocator) GetAvailableAPIPorts() int {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	return pa.apiPortMax - pa.apiPortNext + 1
}

// GetAvailableJanusInstances ritorna numero istanze Janus disponibili
func (pa *PortAllocator) GetAvailableJanusInstances() int {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	httpAvailable := pa.janusHTTPMax - pa.janusHTTPNext + 1
	wsAvailable := pa.janusWSMax - pa.janusWSNext + 1

	// Ritorna il minimo (il collo di bottiglia)
	if httpAvailable < wsAvailable {
		return httpAvailable
	}
	return wsAvailable
}

// GetAvailableWebRTCRanges ritorna numero ranges WebRTC disponibili
func (pa *PortAllocator) GetAvailableWebRTCRanges() int {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	remainingPorts := pa.webrtcRangeMax - pa.webrtcRangeNext + 1
	return remainingPorts / pa.webrtcRangeSize
}

// GetStats ritorna statistiche allocatore
func (pa *PortAllocator) GetStats() map[string]interface{} {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	// Calcola disponibilità Janus (INLINE, no chiamate a funzioni con lock)
	httpAvailable := pa.janusHTTPMax - pa.janusHTTPNext + 1
	wsAvailable := pa.janusWSMax - pa.janusWSNext + 1
	janusAvailable := httpAvailable
	if wsAvailable < httpAvailable {
		janusAvailable = wsAvailable
	}

	// Calcola disponibilità WebRTC ranges (INLINE)
	remainingPorts := pa.webrtcRangeMax - pa.webrtcRangeNext + 1
	webrtcRangesAvailable := remainingPorts / pa.webrtcRangeSize

	return map[string]interface{}{
		"total_used_ports":          len(pa.usedPorts),
		"api_ports_available":       pa.apiPortMax - pa.apiPortNext + 1,
		"api_ports_allocated":       pa.apiPortNext - pa.apiPortMin,
		"janus_instances_available": janusAvailable,
		"janus_http_allocated":      pa.janusHTTPNext - pa.janusHTTPMin,
		"janus_ws_allocated":        pa.janusWSNext - pa.janusWSMin,
		"webrtc_ranges_available":   webrtcRangesAvailable,
	}
}

// Reset resetta allocatore (per test)
func (pa *PortAllocator) Reset() {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	pa.usedPorts = make(map[int]bool)
	pa.apiPortNext = pa.apiPortMin
	pa.janusHTTPNext = pa.janusHTTPMin
	pa.janusWSNext = pa.janusWSMin
	pa.webrtcRangeNext = pa.webrtcRangeStart
}
