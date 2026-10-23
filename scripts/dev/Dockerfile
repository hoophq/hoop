FROM hoophq/agent-tools:noble-20260709-6890906

# A cloud sandbox re-signs TLS with its own CA. run.sh passes it as the
# secret extra_ca when HOOPDEV_CA_CERT is set; without it this step does nothing.
# A secret is not in the build cache key: EXTRA_CA_SHA reruns the step when it changes.
ARG EXTRA_CA_SHA=
RUN --mount=type=secret,id=extra_ca \
  if [ -s /run/secrets/extra_ca ]; then \
    cp /run/secrets/extra_ca /usr/local/share/ca-certificates/hoopdev-extra-ca.crt && \
    update-ca-certificates; \
  fi

RUN mkdir -p /opt/hoop/sessions && \
  mkdir -p /opt/hoop/bin && \
  apt-get update -y && \
  apt-get install -y \
  iproute2 \
  xz-utils \
  openssh-server \
  sudo \
  apt-utils \
  procps

# # SSH Server configuration for testing
RUN echo 'root:1a2b3c4d' | chpasswd && \
  mkdir -p /run/sshd && \
  mkdir -p /root/.ssh && \
  ssh-keygen -A

RUN curl -Lo ec2-metadata-mock "https://github.com/aws/amazon-ec2-metadata-mock/releases/download/v1.13.0/ec2-metadata-mock-linux-$(dpkg --print-architecture)" && \
  chmod +x ec2-metadata-mock && \
  mv ec2-metadata-mock /usr/local/bin/ec2-metadata-mock

COPY rootfs/usr/local/bin /usr/local/bin/
COPY rootfs/opt/hoop/bin /opt/hoop/bin/

ENV PATH="/opt/hoop/bin:${PATH}"

ENTRYPOINT ["tini", "--"]
