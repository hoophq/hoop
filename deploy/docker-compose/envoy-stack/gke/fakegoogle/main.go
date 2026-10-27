// fakegoogle stands in for the Google endpoints a GKE Connect Gateway lane
// touches, on one TLS port, so the stack runs with no Google account:
//
//	POST /tokeninfo                                        oauth2.googleapis.com
//	POST /v1/projects/-/serviceAccounts/*:generateAccessToken  iamcredentials.googleapis.com
//	/v1/projects/*/locations/*/gkeMemberships/*            connectgateway.googleapis.com
//
// and, with -metadata-addr, for the GCE metadata server on plain HTTP:
//
//	GET /computeMetadata/v1/instance/service-accounts/default/token
//
// tokeninfo answers the way Google does for an opaque access token: 200 with
// sub/email/email_verified/exp for a token it knows, 400 invalid_token for
// any other. generateAccessToken answers the way IAM does for the one
// service account the metadata identity may impersonate: an access token the
// gateway half then knows as that service account. The gateway half does
// what Connect Gateway does for kubectl: checks the Google bearer, strips the
// membership prefix, and forwards to the cluster as that user (here a k3s
// static token instead of impersonation), so the cluster's own RBAC still
// decides. Impersonate-* headers pass through to the cluster.
//
// Its certificate is signed by the stack's "customer CA", the one the
// intercepting Envoy also uses, so the sidecar reaches it only through
// trust.ca_file, as it would reach Google through a real MITM.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

// users maps the Google access tokens the demo hands out to the identity
// Google would report and the k3s token Connect Gateway would act as. The
// last one is the token generateAccessToken mints for the sidecar's service
// account; the cluster knows it as a user that may only impersonate.
var users = map[string]struct{ sub, email, k3sToken string }{
	"tok-alice":      {"104771111111111111111", "alice@example.com", "alice-token"},
	"tok-bob":        {"104772222222222222222", "bob@example.com", "bob-token"},
	"tok-sa-gateway": {"105550000000000000001", impersonatedSA, "gateway-token"},
}

// The metadata server's identity and the one service account it may
// impersonate: what GKE Workload Identity gives a pod, and the IAM binding
// roles/iam.serviceAccountTokenCreator on the target account.
const (
	metadataToken  = "tok-sidecar-wi"
	impersonatedSA = "hoop-sidecar@demo-project.iam.gserviceaccount.com"
)

func main() {
	addr := flag.String("addr", ":443", "listen address")
	metadataAddr := flag.String("metadata-addr", "", "plain-HTTP listen address for the metadata server; empty disables it")
	pki := flag.String("pki", "/pki", "directory holding fake-google.crt and fake-google.key")
	k3s := flag.String("k3s", "https://k3s:6443", "the cluster behind the gateway")
	k3sCA := flag.String("k3s-ca", "/k3s/server-ca.crt", "CA that verifies the cluster")
	check := flag.Bool("check", false, "exit 0 when the server on -addr answers; for the healthcheck")
	flag.Parse()

	if *check {
		os.Exit(healthcheck(*addr))
	}

	gateway, err := newGateway(*k3s, *k3sCA)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tokeninfo", tokeninfo)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("POST /v1/projects/-/serviceAccounts/{sa}", generateAccessToken)
	mux.Handle("/v1/", gateway)

	if *metadataAddr != "" {
		md := &http.Server{Addr: *metadataAddr, Handler: http.HandlerFunc(metadata), ReadHeaderTimeout: 10 * time.Second}
		log.Printf("fake-google metadata server on %s", *metadataAddr)
		go func() { log.Fatal(md.ListenAndServe()) }()
	}
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("fake-google on %s (tokeninfo + iamcredentials + connect gateway -> %s)", *addr, *k3s)
	log.Fatal(srv.ListenAndServeTLS(*pki+"/fake-google.crt", *pki+"/fake-google.key"))
}

// metadata is the GCE metadata server's token endpoint: the pod's own
// Workload Identity, which generateAccessToken below accepts. Google requires
// the Metadata-Flavor header and answers with it; both are kept.
func metadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Metadata-Flavor", "Google")
	if r.Header.Get("Metadata-Flavor") != "Google" {
		http.Error(w, "Missing Metadata-Flavor:Google header.", http.StatusForbidden)
		return
	}
	log.Printf("metadata %s %s", r.Method, r.URL.Path)
	switch r.URL.Path {
	case "/computeMetadata/v1/instance/service-accounts/default/token":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": metadataToken,
			"expires_in":   3599,
			"token_type":   "Bearer",
		})
	case "/computeMetadata/v1/instance/service-accounts/default/email":
		fmt.Fprint(w, "sidecar-pod@demo-project.iam.gserviceaccount.com")
	case "/", "/computeMetadata/v1/":
		fmt.Fprintln(w, "computeMetadata/")
	default:
		http.NotFound(w, r)
	}
}

// generateAccessToken is iamcredentials.googleapis.com's
// projects/-/serviceAccounts/{sa}:generateAccessToken: the metadata identity
// may impersonate impersonatedSA and nothing else, as an IAM binding of
// roles/iam.serviceAccountTokenCreator on that one account would allow.
func generateAccessToken(w http.ResponseWriter, r *http.Request) {
	sa, ok := strings.CutSuffix(r.PathValue("sa"), ":generateAccessToken")
	if !ok {
		http.NotFound(w, r)
		return
	}
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case tok != metadataToken:
		log.Printf("generateAccessToken sa=%s: unauthenticated", sa)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`)
	case sa != impersonatedSA:
		log.Printf("generateAccessToken sa=%s: denied", sa)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, `{"error":{"code":403,"message":"Permission 'iam.serviceAccounts.getAccessToken' denied on resource (or it may not exist).","status":"PERMISSION_DENIED"}}`)
	default:
		log.Printf("generateAccessToken sa=%s: ok", sa)
		json.NewEncoder(w).Encode(map[string]string{
			"accessToken": "tok-sa-gateway",
			"expireTime":  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}
}

func tokeninfo(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u, ok := users[r.PostForm.Get("access_token")]
	log.Printf("tokeninfo known=%v query=%q", ok, r.URL.RawQuery)
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_token","error_description":"Invalid Value"}`)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{
		"azp":            "32555940559.apps.googleusercontent.com",
		"sub":            u.sub,
		"email":          u.email,
		"email_verified": "true",
		"exp":            fmt.Sprint(time.Now().Add(time.Hour).Unix()),
		"expires_in":     "3600",
	})
}

// newGateway is the Connect Gateway stand-in: a reverse proxy to the cluster
// that authenticates the Google bearer, drops the membership prefix and acts
// as the matching cluster user. Upgrades (kubectl exec) pass through, which
// httputil.ReverseProxy does on its own for HTTP/1.1.
func newGateway(target, caFile string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s holds no certificate", caFile)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.Out.Host = u.Host
		},
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		FlushInterval: -1,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := stripMembership(r.URL.Path)
		if !ok {
			http.Error(w, "not a gkeMemberships path", http.StatusNotFound)
			return
		}
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		user, ok := users[tok]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"Unauthorized","reason":"Unauthorized","code":401}`)
			return
		}
		log.Printf("gateway %s %s as %s proto=%s via=%q", r.Method, rest, user.email, r.Proto, r.Header.Values("Via"))
		r.URL.Path, r.URL.RawPath = rest, ""
		r.Header.Set("Authorization", "Bearer "+user.k3sToken)
		proxy.ServeHTTP(w, r)
	}), nil
}

// stripMembership removes /v1/projects/P/locations/L/gkeMemberships/M.
func stripMembership(path string) (string, bool) {
	seg := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 8)
	if len(seg) < 7 || seg[0] != "v1" || seg[1] != "projects" || seg[3] != "locations" || seg[5] != "gkeMemberships" {
		return "", false
	}
	if len(seg) == 7 {
		return "/", true
	}
	return "/" + seg[7], true
}

func healthcheck(addr string) int {
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	c := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // loopback liveness only
	}}
	resp, err := c.Get("https://" + host + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
