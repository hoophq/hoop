package http

import (
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// metadataResourcePrefix names the statement metadata key carrying the
// Connect Gateway prefix removed from HTTP.Resource, so the audit trail
// shows that the resource a rule matched is not the path that was sent.
const metadataResourcePrefix = "http.resource_prefix"

// connectGatewaySegments is how many leading segments a Connect Gateway
// path spends naming the cluster:
// <version>/projects/<project>/locations/<location>/<collection>/<membership>.
const connectGatewaySegments = 7

// stripConnectGatewayPrefix rewrites a GKE Connect Gateway resource to the
// Kubernetes resource it addresses.
//
// Connect Gateway fronts a registered cluster's API server at
//
//	https://connectgateway.googleapis.com/v1/projects/P/locations/L/gkeMemberships/M/api/v1/namespaces/N/secrets/S
//
// and forwards everything after the membership to the cluster. A rule is
// written against the Kubernetes path, "/api/v1/namespaces/*/secrets/*",
// and policy compares whole segment lists, so against the full path it
// matches nothing: kubectl through the gateway would pass every such rule
// untouched. That is a rule failing open, and removing the prefix before
// policy sees the statement is what closes it.
//
// Only Resource (and Tables, derived from it) change. Path, Target and Text
// stay as sent, because the audit trail and the analyzer must show the
// request line the client actually wrote. The prefix removed is recorded
// under metadataResourcePrefix.
//
// Anything that is not exactly this shape is left alone: another host, a
// path shorter than the seven segments, or a different literal where the
// gateway expects "projects", "locations" or a membership collection. The
// match is on the host as well as the path so an unrelated API that happens
// to use Google's resource-name layout keeps its own resources.
func stripConnectGatewayPrefix(stmt *inspect.Statement) {
	d := stmt.HTTP
	if d == nil || !isConnectGatewayHost(d.Host) {
		return
	}
	prefix, rest, ok := splitConnectGatewayPath(d.Resource)
	if !ok {
		return
	}
	d.Resource = rest
	// libhoop derives Tables from Resource the same way; a rule on tables
	// must see the resource a rule on resource sees.
	stmt.Tables = []string{strings.ToLower(rest)}
	if stmt.Metadata == nil {
		stmt.Metadata = map[string]string{}
	}
	stmt.Metadata[metadataResourcePrefix] = prefix
}

// isConnectGatewayHost reports whether host is the global Connect Gateway
// endpoint or a regional one (us-central1-connectgateway.googleapis.com).
//
// A port is dropped first, and so is a single trailing dot: "name." is the
// same DNS name as "name", and a client that spells it that way must not
// move its requests out of the rules written for the gateway.
func isConnectGatewayHost(host string) bool {
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "connectgateway.googleapis.com" ||
		strings.HasSuffix(host, "-connectgateway.googleapis.com")
}

// splitConnectGatewayPath splits a normalized resource into the gateway's
// cluster prefix and the Kubernetes resource after it. rest is "/" when the
// request addresses the membership itself.
//
// Leading slashes are skipped the way policy's matcher skips them, so the
// segments tested here are the segments a rule would have been compared
// against. The literals compare case-insensitively, as policy compares
// segments.
func splitConnectGatewayPath(resource string) (prefix, rest string, ok bool) {
	var segs [connectGatewaySegments]string
	remaining := strings.TrimLeft(resource, "/")
	after := ""
	for i := range segs {
		seg, tail, found := strings.Cut(remaining, "/")
		segs[i] = seg
		if !found {
			if i != len(segs)-1 {
				return "", "", false
			}
			break
		}
		remaining = tail
		if i == len(segs)-1 {
			after = tail
		}
	}
	if !oneOf(segs[0], "v1", "v1beta1", "v1alpha1") ||
		!strings.EqualFold(segs[1], "projects") ||
		!strings.EqualFold(segs[3], "locations") ||
		!oneOf(segs[5], "gkeMemberships", "memberships") {
		return "", "", false
	}
	return "/" + strings.Join(segs[:], "/"), "/" + after, true
}

func oneOf(s string, options ...string) bool {
	for _, o := range options {
		if strings.EqualFold(s, o) {
			return true
		}
	}
	return false
}
