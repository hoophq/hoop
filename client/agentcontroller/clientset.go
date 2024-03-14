package agentcontroller

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var defaultLabels = map[string]string{
	"hoop.dev/managed-by": "agentcontroller",
}

func getKubeClientSet() (*kubernetes.Clientset, error) {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, err
		}
		return kubernetes.NewForConfig(config)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	// creates the clientset
	return kubernetes.NewForConfig(config)
}

// defaultLabelSelector returns the first value of defaultLabels
// as key=val string
func defaultLabelSelector() string {
	for key, val := range defaultLabels {
		return fmt.Sprintf("%s=%s", key, val)
	}
	return ""
}

func applyAgentDeployment(deployName, dsnKey, imageRef string, clientset *kubernetes.Clientset) error {
	_, err := clientset.CoreV1().Namespaces().Get(context.Background(), defaultNamespace, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to obtain default namespace: %v", err)
	}

	if apierrors.IsNotFound(err) {
		namespaceSpec := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   defaultNamespace,
				Labels: defaultLabels,
			},
		}
		log.Printf("creating namespace: %v", defaultNamespace)
		_, _ = clientset.CoreV1().Namespaces().Create(context.Background(), namespaceSpec, metav1.CreateOptions{})
	}

	secretsCli := clientset.CoreV1().Secrets(defaultNamespace)
	_ = secretsCli.Delete(context.Background(), deployName, metav1.DeleteOptions{})
	_, err = secretsCli.Create(context.Background(), secretRef(deployName, dsnKey), metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("fail creating secret: %v", err)
	}
	deploymentSpec := agentDeploymentSpec(deployName, imageRef)
	deployCli := clientset.AppsV1().Deployments(defaultNamespace)
	_, err = deployCli.Create(context.Background(), deploymentSpec, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("fail creating deployment: %v", err)
	}
	if apierrors.IsAlreadyExists(err) {
		_, err = deployCli.Update(context.Background(), deploymentSpec, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("fail updating deployment; %v", err)
		}
	}
	return nil
}

type PodStatus struct {
	Name              string               `json:"name"`
	Phase             v1.PodPhase          `json:"phase"`
	StartTime         *metav1.Time         `json:"start_time"`
	PodIP             string               `json:"pod_ip"`
	HostIP            string               `json:"host_ip"`
	ContainerStatuses []v1.ContainerStatus `json:"container_status"`
}

type AgentDeployment struct {
	Name      string                  `json:"name"`
	CreatedAt metav1.Time             `json:"created_at"`
	Status    appsv1.DeploymentStatus `json:"status"`
	PodStatus *PodStatus              `json:"pod_status"`
}

func listAgents(clientset *kubernetes.Clientset) ([]AgentDeployment, error) {
	deploymentList, err := clientset.AppsV1().Deployments(defaultNamespace).List(
		context.Background(),
		metav1.ListOptions{LabelSelector: defaultLabelSelector()},
	)
	if err != nil {
		return nil, err
	}
	podList, err := clientset.CoreV1().Pods(defaultNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: defaultLabelSelector()})
	if err != nil {
		log.Warnf("failed listing pods: %v", err)
	}
	items := []AgentDeployment{}
	for _, obj := range deploymentList.Items {
		podStatus := lookupPod(obj.Name, podList)
		items = append(items, AgentDeployment{
			Name:      obj.Name,
			CreatedAt: obj.CreationTimestamp,
			Status:    obj.Status,
			PodStatus: podStatus,
		})
	}
	return items, nil
}

func lookupPod(matchAppName string, podList *v1.PodList) *PodStatus {
	if podList == nil {
		return nil
	}
	for _, obj := range podList.Items {
		if obj.Labels != nil {
			labelSelectorName := obj.Labels["app"]
			if matchAppName != labelSelectorName {
				continue
			}
			return &PodStatus{
				Name:              obj.Name,
				Phase:             obj.Status.Phase,
				StartTime:         obj.Status.StartTime,
				PodIP:             obj.Status.PodIP,
				HostIP:            obj.Status.HostIP,
				ContainerStatuses: obj.Status.ContainerStatuses,
			}
		}
	}
	return nil
}

func removeDeployment(deployName string, clientset *kubernetes.Clientset) error {
	_ = clientset.CoreV1().Secrets(defaultNamespace).Delete(context.Background(), deployName, metav1.DeleteOptions{})
	_, err := clientset.AppsV1().Deployments(defaultNamespace).Get(context.Background(), deployName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fail obtaining deployment: %v", err)
	}
	return clientset.AppsV1().Deployments(defaultNamespace).Delete(context.Background(), deployName, metav1.DeleteOptions{})
}

func secretRef(name, dsnKey string) *v1.Secret {
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: defaultNamespace,
			Labels:    defaultLabels,
		},
		StringData: map[string]string{
			"HOOP_DSN":     dsnKey,
			"LOG_LEVEL":    "info",
			"LOG_ENCODING": "json",
		},
		Type: v1.SecretTypeOpaque,
	}
}

func agentDeploymentSpec(deployName, imageRef string) *appsv1.Deployment {
	podLabels := map[string]string{}
	for key, val := range defaultLabels {
		podLabels[key] = val
	}
	podLabels["app"] = deployName
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: defaultNamespace,
			Labels:    defaultLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{
				MatchLabels: podLabels,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
					Annotations: map[string]string{
						"checksum/config": uuid.NewString(), // force redeploy on updates
					},
				},
				Spec: v1.PodSpec{
					AutomountServiceAccountToken: bool2Ptr(false),
					Containers: []v1.Container{
						{
							Name:            "hoopagent",
							Image:           imageRef,
							ImagePullPolicy: v1.PullAlways,

							EnvFrom: []v1.EnvFromSource{
								{
									SecretRef: &v1.SecretEnvSource{
										LocalObjectReference: v1.LocalObjectReference{Name: deployName},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func bool2Ptr(v bool) *bool   { return &v }
func int32Ptr(i int32) *int32 { return &i }
