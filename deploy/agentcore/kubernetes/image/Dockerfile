# The harness's environment: kubectl and hoop-kubeconfig, linux/arm64.
# setup.sh builds it with KUBECTL_VERSION at the cluster's minor release.
# The harness replaces ENTRYPOINT and CMD (ENG-880).
FROM public.ecr.aws/docker/library/debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad AS kubectl
ARG KUBECTL_VERSION
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
# The release's published checksum: a download that differs fails the build.
RUN test -n "$KUBECTL_VERSION" \
 && base="https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/arm64" \
 && curl -fsSLo /kubectl "$base/kubectl" \
 && echo "$(curl -fsSL "$base/kubectl.sha256")  /kubectl" | sha256sum -c -

# No curl, no openssl: the agent holds the cluster token, and with an HTTP
# client it could ask for YAML, which the sidecar does not mask. kubectl
# needs no CA bundle: the kubeconfig pins the lane's certificate.
FROM public.ecr.aws/docker/library/debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad
COPY --from=kubectl --chmod=0755 /kubectl /usr/local/bin/kubectl
COPY --chmod=0755 hoop-kubeconfig /usr/local/bin/hoop-kubeconfig
