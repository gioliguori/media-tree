package provisioner

import (
	"fmt"
	"sync"
)

// PortAllocator gestisce allocazione porte per Docker locale
// Previene conflitti tra container assicurando che ogni porta sia usata una sola volta
type PortAllocator struct {
	mu sync.Mutex

	// Mappa delle porte attualmente in uso
	usedPorts map[int]bool

	// range
	apiPortMin int
	apiPortMax int

	janusHTTPMin int
	janusHTTPMax int

	janusWSMin int
	janusWSMax int

	webrtcRangeStart int // 20000
	webrtcRangeSize  int // 100 porte per istanza
	webrtcRangeMax   int // 25000
}

// NewPortAllocator inizializza l'allocatore
func NewPortAllocator() *PortAllocator {
	return &PortAllocator{
		usedPorts: make(map[int]bool),

		// API: 7070-7100 (30 nodi)
		apiPortMin: 7070,
		apiPortMax: 7100,

		// Janus HTTP: 8088-8120 (30 istanze)
		janusHTTPMin: 8088,
		janusHTTPMax: 8120,

		// Janus WS: 8188-8220 (30 istanze)
		janusWSMin: 8188,
		janusWSMax: 8220,

		// WebRTC: 20000-25000 (50 istanze × 100 porte)
		webrtcRangeStart: 20000,
		webrtcRangeSize:  100,
		webrtcRangeMax:   25000,
	}
}

// helper: findFreePort cerca il primo buco libero
func (pa *PortAllocator) findFreePort(min, max int) (int, error) {
	for port := min; port <= max; port++ {
		if !pa.usedPorts[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no ports available in range %d-%d", min, max)
}

// AllocateAPIPort trova una porta API libera
func (pa *PortAllocator) AllocateAPIPort() (int, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	port, err := pa.findFreePort(pa.apiPortMin, pa.apiPortMax)
	if err != nil {
		return 0, fmt.Errorf("API ports exhausted: %w", err)
	}

	pa.usedPorts[port] = true
	return port, nil
}

// AllocateJanusPorts trova due porte (HTTP e WS) libere
func (pa *PortAllocator) AllocateJanusPorts() (httpPort int, wsPort int, err error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	// Trova HTTP
	hPort, err := pa.findFreePort(pa.janusHTTPMin, pa.janusHTTPMax)
	if err != nil {
		return 0, 0, fmt.Errorf("janus http ports exhausted: %w", err)
	}

	// Trova WS
	wPort, err := pa.findFreePort(pa.janusWSMin, pa.janusWSMax)
	if err != nil {
		return 0, 0, fmt.Errorf("janus ws ports exhausted: %w", err)
	}

	// Segna entrambe come usate
	pa.usedPorts[hPort] = true
	pa.usedPorts[wPort] = true

	return hPort, wPort, nil
}

// AllocateWebRTCRange trova un blocco contiguo di 100 porte
func (pa *PortAllocator) AllocateWebRTCRange() (int, int, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	// Scansiona a blocchi: 20000, 20100, 20200...
	for start := pa.webrtcRangeStart; start <= pa.webrtcRangeMax; start += pa.webrtcRangeSize {
		end := start + pa.webrtcRangeSize - 1

		// Controlla se tutte le porte nel blocco sono libere
		isBlockFree := true
		for p := start; p <= end; p++ {
			if pa.usedPorts[p] {
				isBlockFree = false
				break
			}
		}

		if isBlockFree {
			// Blocca tutte le porte del range
			for p := start; p <= end; p++ {
				pa.usedPorts[p] = true
			}
			return start, end, nil
		}
	}

	return 0, 0, fmt.Errorf("no WebRTC ranges available")
}

// Release libera una porta
func (pa *PortAllocator) Release(port int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	delete(pa.usedPorts, port)
}

// ReleaseRange libera un range intero
func (pa *PortAllocator) ReleaseRange(startPort, endPort int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	for p := startPort; p <= endPort; p++ {
		delete(pa.usedPorts, p)
	}
}

// ReleasePorts libera una lista di porte
func (pa *PortAllocator) ReleasePorts(ports ...int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	for _, p := range ports {
		delete(pa.usedPorts, p)
	}
}

// MarkAsUsed forza una porta come usata
func (pa *PortAllocator) MarkAsUsed(port int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	pa.usedPorts[port] = true
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

// helper interno per contare porte libere in un range
func (pa *PortAllocator) countFreePorts(min, max int) int {
	free := 0
	for p := min; p <= max; p++ {
		if !pa.usedPorts[p] {
			free++
		}
	}
	return free
}

// GetAvailableAPIPorts scansiona e conta le porte API libere
func (pa *PortAllocator) GetAvailableAPIPorts() int {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	return pa.countFreePorts(pa.apiPortMin, pa.apiPortMax)
}

// GetAvailableJanusInstances conta quante coppie HTTP+WS complete sono disponibili
func (pa *PortAllocator) GetAvailableJanusInstances() int {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	httpFree := pa.countFreePorts(pa.janusHTTPMin, pa.janusHTTPMax)
	wsFree := pa.countFreePorts(pa.janusWSMin, pa.janusWSMax)

	if httpFree < wsFree {
		return httpFree
	}
	return wsFree
}

// GetAvailableWebRTCRanges conta quanti blocchi da 100 porte sono completamente liberi
func (pa *PortAllocator) GetAvailableWebRTCRanges() int {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	freeRanges := 0
	for start := pa.webrtcRangeStart; start <= pa.webrtcRangeMax; start += pa.webrtcRangeSize {
		end := start + pa.webrtcRangeSize - 1

		// Controlla se tutto il blocco è libero
		isBlockFree := true
		for p := start; p <= end; p++ {
			if pa.usedPorts[p] {
				isBlockFree = false
				break
			}
		}

		if isBlockFree {
			freeRanges++
		}
	}
	return freeRanges
}

// GetStats ritorna statistiche aggiornate
func (pa *PortAllocator) GetStats() map[string]any {

	pa.mu.Lock()
	defer pa.mu.Unlock()

	// usa countFreePorts per evitare problemi sui mutex
	apiFree := pa.countFreePorts(pa.apiPortMin, pa.apiPortMax)

	httpFree := pa.countFreePorts(pa.janusHTTPMin, pa.janusHTTPMax)
	wsFree := pa.countFreePorts(pa.janusWSMin, pa.janusWSMax)
	janusInstances := httpFree
	if wsFree < httpFree {
		janusInstances = wsFree
	}

	webrtcRanges := 0
	for start := pa.webrtcRangeStart; start <= pa.webrtcRangeMax; start += pa.webrtcRangeSize {
		end := start + pa.webrtcRangeSize - 1
		isBlockFree := true
		for p := start; p <= end; p++ {
			if pa.usedPorts[p] {
				isBlockFree = false
				break
			}
		}
		if isBlockFree {
			webrtcRanges++
		}
	}

	return map[string]any{
		"total_used_ports":          len(pa.usedPorts),
		"api_ports_available":       apiFree,
		"janus_instances_available": janusInstances,
		"webrtc_ranges_available":   webrtcRanges,
	}
}

// Reset pulisce tutto (per test)
func (pa *PortAllocator) Reset() {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	pa.usedPorts = make(map[int]bool)
}
