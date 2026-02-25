package metrics

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"controller/internal/redis"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/metrics/pkg/client/clientset/versioned"
)

type K8sMetricsCollector struct {
	metricsClient *versioned.Clientset
	redisClient   *redis.Client
	namespace     string
	stopChan      chan struct{}
}

func NewK8sMetricsCollector(redisClient *redis.Client) (*K8sMetricsCollector, error) {
	// Configurazione per girare dentro il cluster
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s config: %w", err)
	}

	// Creazione del client specifico per le metriche
	mClient, err := versioned.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %w", err)
	}

	return &K8sMetricsCollector{
		metricsClient: mClient,
		redisClient:   redisClient,
		namespace:     "default",
		stopChan:      make(chan struct{}),
	}, nil
}

func (c *K8sMetricsCollector) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	log.Println("[Metrics] K8s Collector started (interval: 10s)")

	go func() {
		for {
			select {
			case <-ticker.C:
				c.collect(ctx)
			case <-c.stopChan:
				ticker.Stop()
				return
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}

func (c *K8sMetricsCollector) collect(ctx context.Context) {
	// Prende le metriche di tutti i pod della mesh tramite le label
	podMetricsList, err := c.metricsClient.MetricsV1beta1().PodMetricses(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app in (injection-pod, relay-pod, egress-pod)",
	})
	if err != nil {
		log.Printf("[Metrics] Error: %v", err)
		return
	}

	pipe := c.redisClient.GetRedisClient().Pipeline()
	now := time.Now().Format(time.RFC3339)

	for _, pm := range podMetricsList.Items {
		podName := pm.Name
		podApp := pm.Labels["app"]

		for _, container := range pm.Containers {
			var nodeId string
			var containerType string

			// riconoscimento container
			switch container.Name {
			case "injection-node":
				nodeId = podName
				containerType = "nodejs"
			case "janus":
				nodeId = podName
				if podApp == "injection-pod" {
					containerType = "janusVideoroom"
				} else {
					containerType = "janusStreaming"
				}

			case "relay-root":

				relayRootId := pm.Labels["relay-root-id"]
				if relayRootId == "" {
					continue
				}
				nodeId = relayRootId
				containerType = "nodejs"
			case "nodejs": // Relay standalone
				nodeId = podName
				containerType = "nodejs"
			case "egress-node":
				nodeId = podName
				containerType = "nodejs"
			default:
				continue
			}

			// Conversione
			// CPU: MilliValue (1000 = 1 core) -> Dividiamo per 10 per avere 0-100% per core.
			cpuUsage := container.Usage.Cpu().MilliValue()
			cpuPercent := float64(cpuUsage) / 10.0

			// RAM Bytes -> Megabytes
			memUsage, _ := container.Usage.Memory().AsInt64()
			memMb := float64(memUsage) / 1024 / 1024

			// Scrittura su redis
			key := fmt.Sprintf("metrics:node:%s:%s", nodeId, containerType)
			pipe.HSet(ctx, key, map[string]any{
				"cpuPercent":   fmt.Sprintf("%.2f", cpuPercent),
				"memoryUsedMb": fmt.Sprintf("%.2f", memMb),
				"containerId":  podName, // containerId = nome del pod
				"timestamp":    now,
			})
			pipe.Expire(ctx, key, 30*time.Second)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[Metrics] Redis pipeline error: %v", err)
	}
}

func (c *K8sMetricsCollector) Stop() {
	close(c.stopChan)
}

func extractNumber(s string) string {
	// Cerca il numero alla fine della stringa
	parts := strings.Split(s, "-")
	return parts[len(parts)-1]
}
