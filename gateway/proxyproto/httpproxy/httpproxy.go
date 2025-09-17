package httpproxy

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
)

const (
	instanceKey      = "http_server"
	cacheKey         = "proxy_connection_cache"
	HoopSecretQuery  = "hoop-secret"
	hoopSecretCookie = "hoop-httpproxy-secret"
)

var (
	instanceStore   sync.Map
	connectionCache sync.Map
)

type proxyConnection struct {
	secret     string
	url        string
	expiration time.Time
}

type proxyServer struct {
	listenAddress string
	httpServer    *http.Server
}

func GetServerInstance() *proxyServer {
	instance, _ := instanceStore.Load(instanceKey)
	if server, ok := instance.(*proxyServer); ok {
		return server
	}
	server := &proxyServer{}
	instanceStore.Store(instanceKey, server)
	return server
}

func (s *proxyServer) Start(listenAddr string) (err error) {
	instance, _ := instanceStore.Load(instanceKey)
	if _, ok := instance.(*proxyServer); ok && s.httpServer != nil {
		return nil
	}

	log.Infof("starting http server proxy at %v", listenAddr)

	// start new instance
	server, err := runProxyServer(listenAddr)
	if err != nil {
		return err
	}
	instanceStore.Store(instanceKey, server)
	return nil
}

func (s *proxyServer) Stop() error {
	instance, _ := instanceStore.LoadAndDelete(instanceKey)
	if proxy, ok := instance.(*proxyServer); ok {
		// close the listener
		if proxy.httpServer != nil {
			log.Infof("stopping http server proxy at %v", proxy.httpServer.Addr)
			_ = proxy.httpServer.Close()
		}
	}
	return nil
}

func validateRequestSecret(secret string) (*proxyConnection, error) {
	if secret == "" {
		return nil, fmt.Errorf("missing secret access key")
	}

	// Check cache first
	cachedConn, _ := connectionCache.Load(cacheKey)
	if connectionCache, ok := cachedConn.(*proxyConnection); ok {
		if !connectionCache.expiration.After(time.Now().UTC()) {
			return nil, fmt.Errorf("secret access key expired")
		}

		return connectionCache, nil
	}

	// Validate received the secret
	secretKeyHash, err := keys.Hash256Key(secret)
	if err != nil {
		return nil, err
	}

	dba, err := models.GetValidConnectionCredentialsBySecretKey(pb.ConnectionTypeHttpProxy.String(), secretKeyHash)
	if err != nil {
		log.Errorf("failed to get connection credentials by secret key, err=%v", err)
		return nil, err
	}

	if dba.ExpireAt.Before(time.Now().UTC()) {
		return nil, fmt.Errorf("secret access key expired")
	}

	adminCtx := models.NewAdminContext(dba.OrgID)
	conn, err := models.GetConnectionByNameOrID(adminCtx, dba.ConnectionName)
	if err != nil || conn == nil {
		log.Errorf("failed to get connection by name, name=%v, err=%v", dba.ConnectionName, err)
		return nil, fmt.Errorf("failed to get connection by name, name=%v, err=%v", dba.ConnectionName, err)
	}

	// validation
	remoteURL, _ := base64.StdEncoding.DecodeString(conn.Envs["envvar:REMOTE_URL"])

	connection := &proxyConnection{
		secret:     secret,
		url:        string(remoteURL),
		expiration: dba.ExpireAt,
	}
	connectionCache.Store(cacheKey, connection)

	return connection, nil
}

func requestHandler(w http.ResponseWriter, r *http.Request) {
	// Retrieve the connection credentials
	secretProvidedViaQuery := true
	secret := r.URL.Query().Get(HoopSecretQuery)
	if secret == "" {
		// so provided via cookie
		secretProvidedViaQuery = false

		cookie, _ := r.Cookie(hoopSecretCookie)
		if cookie != nil {
			secret = cookie.Value
		}
	}

	// validate the secret
	conn, err := validateRequestSecret(secret)
	if err != nil {
		log.Debugf("invalid secret token, err=%v", err)

		http.Error(w, "invalid secret token", http.StatusForbidden)
		return
	}

	// Log the incoming request
	log.Infof("Proxying request: %s %s", r.Method, r.URL.String())

	// Proxy below
	target, _ := url.Parse(conn.url)

	proxy := httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)

			// Remove the hoop-secret query parameter before forwarding
			if secretProvidedViaQuery {
				query := r.In.URL.Query()
				query.Del(HoopSecretQuery)
				r.Out.URL.RawQuery = query.Encode()
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if secretProvidedViaQuery {
				hoopCookie := &http.Cookie{
					Name:     hoopSecretCookie,
					Value:    secret,
					Path:     "/",
					Expires:  conn.expiration,
					HttpOnly: true,
					Secure:   true,
				}
				resp.Header.Add("Set-Cookie", hoopCookie.String())
			}

			return nil
		},
	}

	proxy.ServeHTTP(w, r)
}

func runProxyServer(listenAddr string) (*proxyServer, error) {
	httpServer := &http.Server{
		Addr:    listenAddr,
		Handler: http.HandlerFunc(requestHandler),
	}

	httpProxy := &proxyServer{
		listenAddress: listenAddr,
		httpServer:    httpServer,
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil {
			log.Errorf("failed to start http server, err=%v", err)
		}
	}()

	return httpProxy, nil
}
