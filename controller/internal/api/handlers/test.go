package handlers

// import (
// 	"fmt"
// 	"net/http"

// 	"github.com/gin-gonic/gin"

// 	"controller/internal/domain"
// 	"controller/internal/provisioner"
// )

// type TestHandler struct {
// 	portAllocator *provisioner.PortAllocator
// }

// func NewTestHandler() *TestHandler {
// 	return &TestHandler{
// 		portAllocator: provisioner.NewPortAllocator(),
// 	}
// }

// // TestDomainModels testa le strutture domain
// func (h *TestHandler) TestDomainModels(c *gin.Context) {
// 	// Crea NodeSpec
// 	spec := domain.NodeSpec{
// 		NodeId:   "test-injection-1",
// 		NodeType: domain.NodeTypeInjection,
// 		TreeId:   "test-tree-1",
// 		Layer:    0,
// 	}

// 	// Simula NodeInfo
// 	info := domain.NodeInfo{
// 		NodeId:           spec.NodeId,
// 		NodeType:         spec.NodeType,
// 		TreeId:           spec.TreeId,
// 		Layer:            spec.Layer,
// 		ContainerId:      "abc123",
// 		InternalHost:     "test-injection-1",
// 		InternalAPIPort:  7070,
// 		InternalRTPAudio: 5000,
// 		InternalRTPVideo: 5002,
// 		ExternalHost:     "localhost",
// 		ExternalAPIPort:  7070,
// 		JanusContainerId: "def456",
// 		JanusHost:        "test-injection-1-janus-vr",
// 		JanusWSPort:      8188,
// 		JanusHTTPPort:    8088,
// 	}

// 	c.JSON(http.StatusOK, gin.H{
// 		"spec": spec,
// 		"info": info,
// 		"checks": gin.H{
// 			"isInjection": info.IsInjection(),
// 			"isRelay":     info.IsRelay(),
// 			"isEgress":    info.IsEgress(),
// 			"needsJanus":  info.NeedsJanus(),
// 		},
// 	})
// }

// // TestPortAllocator testa allocazione base
// func (h *TestHandler) TestPortAllocator(c *gin.Context) {

// 	// Alloca 3 porte API
// 	api1, _ := h.portAllocator.AllocateAPIPort()
// 	api2, _ := h.portAllocator.AllocateAPIPort()
// 	api3, _ := h.portAllocator.AllocateAPIPort()

// 	// Alloca 2 set Janus
// 	janusHTTP1, janusWS1, _ := h.portAllocator.AllocateJanusPorts()
// 	janusHTTP2, janusWS2, _ := h.portAllocator.AllocateJanusPorts()

// 	c.JSON(http.StatusOK, gin.H{
// 		"apiPorts": []int{api1, api2, api3},
// 		"janusPorts": []map[string]int{
// 			{"http": janusHTTP1, "ws": janusWS1},
// 			{"http": janusHTTP2, "ws": janusWS2},
// 		},
// 		"usedPorts":      h.portAllocator.GetUsedPorts(),
// 		"availablePorts": h.portAllocator.GetAvailableAPIPorts(),
// 	})
// }

// // TestPortAllocation simula allocazione 3 nodi
// func (h *TestHandler) TestPortAllocation(c *gin.Context) {

// 	nodes := []map[string]any{}

// 	for i := 1; i <= 3; i++ {
// 		apiPort, _ := h.portAllocator.AllocateAPIPort()

// 		node := map[string]any{
// 			"nodeId":  fmt.Sprintf("node-%d", i),
// 			"apiPort": apiPort,
// 		}

// 		// Injection o Egress: aggiungi Janus
// 		if i == 1 || i == 3 {
// 			janusHTTP, janusWS, _ := h.portAllocator.AllocateJanusPorts()
// 			node["janusHTTP"] = janusHTTP
// 			node["janusWS"] = janusWS
// 		}

// 		nodes = append(nodes, node)
// 	}

// 	c.JSON(http.StatusOK, gin.H{
// 		"nodes":      nodes,
// 		"usedPorts": h.portAllocator.GetUsedPorts(),
// 		"summary": gin.H{
// 			"totalAllocated": len(h.portAllocator.GetUsedPorts()),
// 			"availableApi":   h.portAllocator.GetAvailableAPIPorts(),
// 		},
// 	})
// }

// // TestPortRelease testa rilascio porta
// func (h *TestHandler) TestPortRelease(c *gin.Context) {

// 	port1, _ := h.portAllocator.AllocateAPIPort()
// 	port2, _ := h.portAllocator.AllocateAPIPort()
// 	port3, _ := h.portAllocator.AllocateAPIPort()

// 	usedBefore := h.portAllocator.GetUsedPorts()

// 	h.portAllocator.Release(port2)

// 	usedAfter := h.portAllocator.GetUsedPorts()

// 	c.JSON(http.StatusOK, gin.H{
// 		"allocated":   []int{port1, port2, port3},
// 		"released":    port2,
// 		"usedBefore": usedBefore,
// 		"usedAfter":  usedAfter,
// 	})
// }

// // TestWebRTCAllocation testa allocazione WebRTC ranges
// func (h *TestHandler) TestWebRTCAllocation(c *gin.Context) {
// 	h.portAllocator.Reset()

// 	instances := []map[string]any{}

// 	for i := 1; i <= 3; i++ {
// 		httpPort, wsPort, err := h.portAllocator.AllocateJanusPorts()
// 		if err != nil {
// 			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
// 			return
// 		}

// 		webrtcStart, webrtcEnd, err := h.portAllocator.AllocateWebRTCRange()
// 		if err != nil {
// 			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
// 			return
// 		}

// 		instances = append(instances, map[string]any{
// 			"instance":     fmt.Sprintf("janus-%d", i),
// 			"httpPort":    httpPort,
// 			"wsPort":      wsPort,
// 			"webrtcStart": webrtcStart,
// 			"webrtcEnd":   webrtcEnd,
// 			"webrtcCount": webrtcEnd - webrtcStart + 1,
// 		})
// 	}

// 	c.JSON(http.StatusOK, gin.H{
// 		"instances":  instances,
// 		"usedPorts": h.portAllocator.GetUsedPorts(),
// 		"stats":      h.portAllocator.GetStats(),
// 	})
// }

// // TestFullTreeAllocation simula tree completo
// func (h *TestHandler) TestFullTreeAllocation(c *gin.Context) {
// 	h.portAllocator.Reset()

// 	nodes := []map[string]any{}

// 	// INJECTION
// 	injectionAPI, _ := h.portAllocator.AllocateAPIPort()
// 	janusVRHTTP, janusVRWS, _ := h.portAllocator.AllocateJanusPorts()
// 	webrtcVRStart, webrtcVREnd, _ := h.portAllocator.AllocateWebRTCRange()

// 	nodes = append(nodes, map[string]any{
// 		"nodeId":      "injection-1",
// 		"type":         "injection",
// 		"apiPort":     injectionAPI,
// 		"janusHttp":   janusVRHTTP,
// 		"janusWs":     janusVRWS,
// 		"webrtcStart": webrtcVRStart,
// 		"webrtcEnd":   webrtcVREnd,
// 	})

// 	// RELAY
// 	relayAPI, _ := h.portAllocator.AllocateAPIPort()

// 	nodes = append(nodes, map[string]any{
// 		"nodeId":  "relay-1",
// 		"type":     "relay",
// 		"apiPort": relayAPI,
// 	})

// 	// EGRESS
// 	egressAPI, _ := h.portAllocator.AllocateAPIPort()
// 	janusStreamHTTP, janusStreamWS, _ := h.portAllocator.AllocateJanusPorts()
// 	webrtcStreamStart, webrtcStreamEnd, _ := h.portAllocator.AllocateWebRTCRange()

// 	nodes = append(nodes, map[string]any{
// 		"nodeId":      "egress-1",
// 		"type":         "egress",
// 		"apiPort":     egressAPI,
// 		"janusHttp":   janusStreamHTTP,
// 		"janusWs":     janusStreamWS,
// 		"webrtcStart": webrtcStreamStart,
// 		"webrtcEnd":   webrtcStreamEnd,
// 	})

// 	c.JSON(http.StatusOK, gin.H{
// 		"treeId":    "tree-1",
// 		"nodes":      nodes,
// 		"totalUsed": len(h.portAllocator.GetUsedPorts()),
// 		"stats":      h.portAllocator.GetStats(),
// 	})
// }

// // TestReleaseWebRTCRange testa rilascio range
// func (h *TestHandler) TestReleaseWebRTCRange(c *gin.Context) {
// 	h.portAllocator.Reset()

// 	start1, end1, _ := h.portAllocator.AllocateWebRTCRange()
// 	start2, end2, _ := h.portAllocator.AllocateWebRTCRange()
// 	start3, end3, _ := h.portAllocator.AllocateWebRTCRange()

// 	allocated := []map[string]int{
// 		{"start": start1, "end": end1},
// 		{"start": start2, "end": end2},
// 		{"start": start3, "end": end3},
// 	}

// 	usedBefore := len(h.portAllocator.GetUsedPorts())

// 	h.portAllocator.ReleaseRange(start2, end2)

// 	usedAfter := len(h.portAllocator.GetUsedPorts())

// 	c.JSON(http.StatusOK, gin.H{
// 		"allocated":   allocated,
// 		"released":    map[string]int{"start": start2, "end": end2},
// 		"usedBefore": usedBefore,
// 		"usedAfter":  usedAfter,
// 		"freedCount": usedBefore - usedAfter,
// 	})
// }
