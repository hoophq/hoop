#!/bin/sh
#
# Runs in the sandbox, once per session: installs kubectl from the tarball
# next to it and writes the agent's kubeconfig.
#
#   sh /workspace/.hoop/kubeconfig.sh <session id>
#
# kubectl cannot add a header, but it sends impersonation headers from its
# kubeconfig: the session id goes out as Impersonate-Extra-Hoop-Session, and
# the sidecar records it in each row (../rbac.yaml).

set -eu

SIDECAR_HOST=sidecar.example.com # setup.sh writes the real host
AGENT=system:serviceaccount:hoop:openai-agent # EDIT with ../rbac.yaml
NAMESPACE=demo # EDIT with ../rbac.yaml

# The id goes into YAML: accept its exact form only. A pattern, not grep,
# which would pass an id with a second line.
case "${1:-}" in
    sess_ | sess_*[!A-Za-z0-9]*) ;;
    sess_*) SESSION=$1 ;;
esac
[ -n "${SESSION:-}" ] || { echo "usage: $0 <session id> (sess_...): the first message gives it" >&2; exit 2; }
[ -n "${KUBE_TOKEN:-}" ] || { echo "KUBE_TOKEN is not set: start the session with the vault" >&2; exit 1; }

if ! command -v kubectl >/dev/null; then
    tar -xzf "$(dirname "$0")/kubernetes-client-linux-amd64.tar.gz" -O kubernetes/client/bin/kubectl \
        > /usr/local/bin/kubectl
    chmod 0755 /usr/local/bin/kubectl
fi

# No certificate-authority: kubectl goes through HTTPS_PROXY, which inspects
# TLS, and the system bundle trusts its CA (ENG-860 c4). demo.sh points
# KUBECONFIG elsewhere; the sandbox sets none.
CONFIG=${KUBECONFIG:-$HOME/.kube/config}
mkdir -p "$(dirname "$CONFIG")"
(umask 077; cat > "$CONFIG") <<YAML
apiVersion: v1
kind: Config
clusters: [{name: hoop, cluster: {server: 'https://$SIDECAR_HOST'}}]
users:
  - name: agent
    user:
      token: '$KUBE_TOKEN'
      as: $AGENT
      as-user-extra: {hoop-session: ['$SESSION']}
contexts: [{name: hoop, context: {cluster: hoop, user: agent, namespace: $NAMESPACE}}]
current-context: hoop
YAML
echo "kubectl $(kubectl version --client | sed -n 's/^Client Version: //p') reaches https://$SIDECAR_HOST as session $SESSION"
