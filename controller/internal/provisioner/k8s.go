package provisioner

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"sync"
	"text/template"
	"time"

	"controller/internal/domain"
	"controller/internal/redis"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

//go:embed templates/*.yaml
var templateFS embed.FS

type AgentConfig struct {
	Name     string
	PublicIP string
	ExtPort  int
	WebRTC   string
}

type K8sProvisioner struct {
	clientset      *kubernetes.Clientset
	namespace      string
	redisClient    *redis.Client
	agents         []AgentConfig
	mu             sync.Mutex
	reservedAgents map[string]bool
}

func NewK8sProvisioner(redisClient *redis.Client) (*K8sProvisioner, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s clientset: %w", err)
	}

	publicIP := os.Getenv("MY_IP")
	if publicIP == "" {
		publicIP = "127.0.0.1"
	}

	labAgents := []AgentConfig{
		{Name: "k3d-media-tree-agent-0", PublicIP: publicIP, ExtPort: 11000, WebRTC: "20000-20010"},
		{Name: "k3d-media-tree-agent-1", PublicIP: publicIP, ExtPort: 12000, WebRTC: "20100-20110"},
		{Name: "k3d-media-tree-agent-2", PublicIP: publicIP, ExtPort: 13000, WebRTC: "20200-20210"},
	}

	return &K8sProvisioner{
		clientset:      clientset,
		namespace:      "default",
		redisClient:    redisClient,
		agents:         labAgents,
		reservedAgents: make(map[string]bool),
	}, nil
}

func (p *K8sProvisioner) CreateNode(ctx context.Context, spec domain.NodeSpec, role string) (*domain.NodeInfo, error) {
	log.Printf("[K8s] Provisioning started for %s (%s)", spec.NodeId, spec.NodeType)
	// Trova un nodo fisico libero
	p.mu.Lock()
	agent, err := p.scheduleAndAllocate(ctx, spec.NodeType)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if agent.Name != "" {
		p.reservedAgents[agent.Name] = true
	}
	p.mu.Unlock()

	// Rilascia la prenotazione quando CreateNode ritorna
	defer func() {
		if agent.Name != "" {
			p.mu.Lock()
			delete(p.reservedAgents, agent.Name)
			p.mu.Unlock()
		}
	}()

	// Prepara gli argomenti per il template
	data := map[string]any{
		"NodeId":       spec.NodeId,
		"NodeType":     string(spec.NodeType),
		"RelayRootId":  spec.RelayRootId,
		"Role":         role,
		"SelectedNode": agent.Name,
		"WebRTCRange":  agent.WebRTC,
		"PublicIP":     agent.PublicIP,
		"ApiPort":      agent.ExtPort,
	}

	tmpl, err := template.ParseFS(templateFS, "templates/"+string(spec.NodeType)+".yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to load template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	// Decode YAML -> Oggetto Kubernetes Pod
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode(buf.Bytes(), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode yaml: %w", err)
	}
	pod := obj.(*corev1.Pod)

	// Creazione fisica del Pod su K8s
	_, err = p.clientset.CoreV1().Pods(p.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("k8s creation failed: %w", err)
	}

	// Attesa Pod Ready e assegnazione IP
	podStatus, err := p.waitForPodReady(ctx, spec.NodeId)
	if err != nil {
		// Se fallisce l'assegnazione, puliamo K8s
		_ = p.clientset.CoreV1().Pods(p.namespace).Delete(ctx, spec.NodeId, metav1.DeleteOptions{})
		return nil, err
	}

	// Costruzione del NodeInfo
	nodeInfo := &domain.NodeInfo{
		NodeId:           spec.NodeId,
		NodeType:         spec.NodeType,
		Role:             role,
		MaxSlots:         spec.MaxSlots,
		ContainerId:      spec.NodeId, // In K8s usiamo Pod Name come ID univoco
		InternalAPIPort:  agent.ExtPort,
		InternalRTPAudio: 5002,
		InternalRTPVideo: 5004,
		ExternalHost:     "localhost",
		ExternalAPIPort:  agent.ExtPort,
		CreatedAt:        time.Now().Unix(),
	}

	// Differenziazione rete tra nodi Interni (Relay) e Privilegiati (Injection/Egress)
	if spec.NodeType == domain.NodeTypeRelay {
		nodeInfo.InternalHost = podStatus.Status.PodIP // IP privato del Pod
	} else {
		nodeInfo.InternalHost = podStatus.Status.HostIP // IP dell'Agent k3d (visibile agli altri agenti)
		nodeInfo.JanusHost = "localhost"
		nodeInfo.JanusHTTPPort = 8088
		nodeInfo.JanusWSPort = 8188
	}

	// Salvataggio su Redis
	if err := p.redisClient.SaveNodeProvisioning(ctx, nodeInfo); err != nil {
		_ = p.clientset.CoreV1().Pods(p.namespace).Delete(ctx, spec.NodeId, metav1.DeleteOptions{})
		return nil, fmt.Errorf("failed to save to redis: %w", err)
	}

	// NodeInfo Relayroot
	if spec.NodeType == domain.NodeTypeInjection && spec.RelayRootId != "" {
		rootInfo := &domain.NodeInfo{
			NodeId:           spec.RelayRootId,
			NodeType:         domain.NodeTypeRelay,
			Role:             "root",
			MaxSlots:         spec.MaxSlots,
			ContainerId:      spec.NodeId,             // Stesso Pod
			InternalHost:     podStatus.Status.HostIP, // Stesso host del Pod Injection
			InternalAPIPort:  7071,                    // Porta differenziata
			InternalRTPAudio: 5002,
			InternalRTPVideo: 5004,
			ExternalHost:     "localhost",
			ExternalAPIPort:  0,
			CreatedAt:        time.Now().Unix(),
		}

		// Salviamo su Redis
		if err := p.redisClient.SaveNodeProvisioning(ctx, rootInfo); err != nil {
			_ = p.clientset.CoreV1().Pods(p.namespace).Delete(ctx, spec.NodeId, metav1.DeleteOptions{})
			return nil, err
		}
		log.Printf("[K8s] Provisioned Injection Pair: %s (7070) <-> %s (7071)", spec.NodeId, spec.RelayRootId)
	}

	log.Printf("[K8s] Provisioned %s on %s (Ext Port: %d)", spec.NodeId, podStatus.Spec.NodeName, nodeInfo.ExternalAPIPort)
	return nodeInfo, nil
}

// scheduleAndAllocate: Cerca un agente libero per Ingress/Egress e mappa le porte
func (p *K8sProvisioner) scheduleAndAllocate(ctx context.Context, nType domain.NodeType) (AgentConfig, error) {
	if nType == domain.NodeTypeRelay {
		return AgentConfig{}, nil
	}

	// Elenco agenti occupati
	pods, _ := p.clientset.CoreV1().Pods(p.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app in (injection-pod, egress-pod)",
	})

	occupied := make(map[string]bool)
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != "" {
			occupied[pod.Spec.NodeName] = true
		}
	}

	for _, agent := range p.agents {
		if !occupied[agent.Name] && !p.reservedAgents[agent.Name] {
			return agent, nil
		}
	}
	return AgentConfig{}, fmt.Errorf("no physical agents available")
}

// waitForPodReady aspetta che K8s assegni un IP al Pod
func (p *K8sProvisioner) waitForPodReady(ctx context.Context, name string) (*corev1.Pod, error) {
	for range 120 {
		pod, err := p.clientset.CoreV1().Pods(p.namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && pod.Status.PodIP != "" && pod.Status.Phase == corev1.PodRunning {
			return pod, nil
		}
		time.Sleep(1 * time.Second)
	}
	return nil, fmt.Errorf("pod %s failed to become ready (timeout)", name)
}

func (p *K8sProvisioner) Close() error { return nil }

func (p *K8sProvisioner) DestroyNode(ctx context.Context, nodeInfo *domain.NodeInfo) error {
	log.Printf("[K8s] Destroying node %s", nodeInfo.NodeId)

	// Se mancano info recupera da Redis
	if nodeInfo.NodeType == "" {
		fullInfo, err := p.redisClient.GetNodeProvisioning(ctx, nodeInfo.NodeId)
		if err == nil {
			nodeInfo = fullInfo
		}
	}

	if nodeInfo.Role == "root" {
		log.Printf("[K8s] Node %s is a virtual root, cleaning logic only", nodeInfo.NodeId)
		p.redisClient.ForceDeleteNode(ctx, nodeInfo.NodeId, string(nodeInfo.NodeType))
		if err := p.redisClient.DeleteNodeProvisioning(ctx, nodeInfo.NodeId); err != nil {
			log.Printf("[WARN] Failed to delete provisioning info from Redis: %v", err)
		}
		return nil
	}

	// Rimuovi Pod da Kubernetes
	err := p.clientset.CoreV1().Pods(p.namespace).Delete(ctx, nodeInfo.NodeId, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("[WARN] Failed to delete Pod %s: %v", nodeInfo.NodeId, err)
	}

	// Cleanup Redis
	if nodeInfo.NodeType != "" {
		p.redisClient.ForceDeleteNode(ctx, nodeInfo.NodeId, string(nodeInfo.NodeType))
	}

	// Rimuove info di provisioning
	if err := p.redisClient.DeleteNodeProvisioning(ctx, nodeInfo.NodeId); err != nil {
		log.Printf("[WARN] Failed to delete provisioning info from Redis: %v", err)
	}

	log.Printf("[K8s] Node %s destroyed successfully", nodeInfo.NodeId)
	return nil
}
