package agentcontroller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/runopsio/hoop/common/agentcontroller"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/version"
)

const (
	defaultImageRef  = "docker.io/hoophq/hoopdev:latest"
	defaultNamespace = "hoopagents"
)

func isValidateAgentRequest(r *agentcontroller.AgentRequest, w http.ResponseWriter) (valid bool) {
	if r.DSNKey == "" || r.Name == "" || r.ID == "" {
		httpError(w, http.StatusBadRequest, `'dsn_key', 'id' and 'name' attributes are required`)
		return
	}
	// it must be a safe length to avoid problems with limitation names on Kubernetes
	if len(r.Name) > 45 {
		httpError(w, http.StatusBadRequest, `'name' attribute max size reach (45 characters)`)
		return
	}
	r.Name = strings.ToLower(r.Name)

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
	var req agentcontroller.AgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "fail decoding request body, reason=%v", err)
		return
	}

	if !isValidateAgentRequest(&req, w) {
		return
	}

	log.Printf("deploying agent %v, image=%v, ua=%v", req.Name, req.ImageRef, r.Header.Get("user-agent"))
	clientset, err := getKubeClientSet()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "fail obtaining cluster clientset, reason=%v", err)
		return
	}
	if err := applyAgentDeployment(req, clientset); err != nil {
		httpError(w, http.StatusInternalServerError, "fail creating deployment %s, reason=%v", req.Name, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).
		Encode(&agentcontroller.AgentResponse{Deployment: req.Name})
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

	vi := version.Get()
	log.Infof("starting agent controller at port :8015. version=%v, goversion=%v", vi.Version, vi.GoVersion)
	log.Info("PUT /api/agents")
	log.Info("GET /api/agents")
	log.Info("DELETE /api/agents/{name}?id=")
	log.Info("GET /api/healthz")
	if err := http.ListenAndServe(":8015", mux); err != nil {
		log.Fatalf("failed starting api server, err=%v", err)
	}
}
