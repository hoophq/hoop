package agentcontroller

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"os"
	"strings"

	"github.com/runopsio/hoop/common/log"
)

const (
	defaultImageRef  = "docker.io/hoophq/hoopdev:latest"
	defaultNamespace = "hoopagents"
)

type AgentRequest struct {
	ID         string `json:"id"`
	DeployName string `json:"name"`
	DSNKey     string `json:"dsn_key"`
	ImageRef   string `json:"image"`
}

func (r *AgentRequest) IsValid(w http.ResponseWriter) (valid bool) {
	if r.DSNKey == "" || r.DeployName == "" || r.ID == "" {
		httpError(w, http.StatusBadRequest, `'dsn_key', 'id' and 'name' attributes are required`)
		return
	}
	if len(r.DeployName) > 45 {
		httpError(w, http.StatusBadRequest, `'name' attribute max size reach (45 characters)`)
		return
	}
	r.DeployName = strings.ToLower(r.DeployName)

	if r.ImageRef == "" {
		r.ImageRef = defaultImageRef
	}
	return true
}

func deployNameHash(name, id string) string {
	t := crc32.MakeTable(crc32.IEEE)
	return fmt.Sprintf("%s-%08x", name, crc32.Checksum([]byte(id), t))
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

func agentListHandler(w http.ResponseWriter, r *http.Request) {
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
	deployName := r.PathValue("name")
	id := r.URL.Query().Get("id")
	if len(id) == 0 {
		httpError(w, http.StatusBadRequest, "id query string is missing")
		return
	}
	deployName = deployNameHash(deployName, id)
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

func agentPutHandler(w http.ResponseWriter, r *http.Request) {
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

	deployName := deployNameHash(req.DeployName, req.ID)
	log.Printf("deploying agent %v, image=%v, ua=%v", deployName, req.ImageRef, r.Header.Get("user-agent"))
	clientset, err := getKubeClientSet()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "fail obtaining cluster clientset, reason=%v", err)
		return
	}
	if err := applyAgentDeployment(deployName, req.DSNKey, req.ImageRef, clientset); err != nil {
		httpError(w, http.StatusInternalServerError, "fail creating deployment %s, reason=%v", deployName, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"deployment": deployName,
		"namespace":  defaultNamespace,
	})
}

func RunServer() {
	jwtSecretKey := os.Getenv("JWT_KEY")
	if len(jwtSecretKey) < 40 {
		log.Fatalf("JWT_KEY must be at least 40 characters")
	}
	auth := NewAuthMiddleware(jwtSecretKey)

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/agents", auth.Handler(agentPutHandler))
	mux.HandleFunc("GET /api/agents", auth.Handler(agentListHandler))
	mux.HandleFunc("DELETE /api/agents/{name}", auth.Handler(agentDeleteHandler))
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "OK",
		})
	})

	log.Println("listening api on port :8015")
	log.Println("PUT /api/agents")
	log.Println("GET /api/agents")
	log.Println("DELETE /api/agents/{name}?id=")
	log.Println("GET /api/healthz")
	if err := http.ListenAndServe(":8015", mux); err != nil {
		log.Fatalf("failed starting api server, err=%v", err)
	}
}
