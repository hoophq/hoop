package agentcontroller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/runopsio/hoop/common/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultImageRef  = "docker.io/hoophq/hoopdev:latest"
	defaultNamespace = "hoopagents"
)

var defaultLabels = map[string]string{
	"app.kubernetes.io/managed-by": "agentcontroller",
}

type AgentRequest struct {
	DSNKey     string `json:"dsn_key"`
	DeployName string `json:"name"`
	ImageRef   string `json:"image"`
}

func (r *AgentRequest) IsValid(w http.ResponseWriter) (valid bool) {
	if r.DSNKey == "" || r.DeployName == "" {
		httpError(w, http.StatusBadRequest, `'dsn_key' and 'name' attributes are required`)
		return
	}
	if r.ImageRef == "" {
		r.ImageRef = defaultImageRef
	}
	return true
}

func httpError(w http.ResponseWriter, code int, msg string, a ...any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	log.Printf("responding with error, code=%v, message=%v", code, fmt.Sprintf(msg, a...))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"message": fmt.Sprintf(msg, a...),
		"code":    code,
	})
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

func agentHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		handleList(w)
	case "PUT":
		handlePut(w, r)
	default:
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleList(w http.ResponseWriter) {
	clientset, err := getKubeClientSet()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "fail obtaining cluster clientset, reason=%v", err)
		return
	}
	itemList, err := listAgents(clientset)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed listing deployments, reason=%v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(itemList)
	if err != nil {
		log.Printf("failed encoding deployment items, reason=%v", err)
	}
}

func agentDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	deployName := r.PathValue("name")
	clientset, err := getKubeClientSet()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "fail obtaining cluster clientset, reason=%v", err)
		return
	}
	log.Printf("removing agent %v, ua=%v", deployName, r.Header.Get("user-agent"))
	if err := removeDeployment(deployName, clientset); err != nil {
		httpError(w, http.StatusInternalServerError, "fail removing deployment, reason=%v", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handlePut(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		httpError(w, http.StatusUnsupportedMediaType, "unsupported media type")
		return
	}
	var req AgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "fail decoding request body, reason=%v", err)
		return
	}

	if !req.IsValid(w) {
		return
	}

	log.Printf("deploying agent %v, image=%v, ua=%v", req.DeployName, req.ImageRef, r.Header.Get("user-agent"))
	clientset, err := getKubeClientSet()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "fail obtaining cluster clientset, reason=%v", err)
		return
	}
	if err := applyAgentDeployment(req.DeployName, req.DSNKey, req.ImageRef, clientset); err != nil {
		httpError(w, http.StatusInternalServerError, "fail creating deployment %s, reason=%v", req.DeployName, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"deployment": req.DeployName,
		"namespace":  defaultNamespace,
	})
}

func RunServer() {
	// TODO: make requests authenticated
	apiKey := os.Getenv("APIKEY")
	if apiKey == "" {
		log.Fatalf("APIKEY env not set")
	}
	http.HandleFunc("/api/agents", agentHandler)
	http.HandleFunc("/api/agents/{name}", agentDeleteHandler)
	log.Println("listening api on port :8015")
	log.Println("POST /api/agents")
	log.Println("GET /api/agents")
	log.Println("DELETE /api/agents/{name}")
	if err := http.ListenAndServe(":8015", nil); err != nil {
		log.Fatalf("failed starting api server, err=%v", err)
	}
}
