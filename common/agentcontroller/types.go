package agentcontroller

import (
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DSNKey   string `json:"dsn_key"`
	ImageRef string `json:"image"`
}

type AgentResponse struct {
	Deployment string `json:"deployment"`
}

type PodStatus struct {
	Name              string               `json:"name"`
	Phase             v1.PodPhase          `json:"phase"`
	StartTime         *metav1.Time         `json:"start_time"`
	PodIP             string               `json:"pod_ip"`
	HostIP            string               `json:"host_ip"`
	ContainerStatuses []v1.ContainerStatus `json:"container_status"`
}

type Deployment struct {
	Name      string                  `json:"name"`
	CreatedAt metav1.Time             `json:"created_at"`
	Status    appsv1.DeploymentStatus `json:"status"`
	PodStatus *PodStatus              `json:"pod_status"`
}
