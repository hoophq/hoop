FROM hoophq/agent-tools:noble-20260709-6890906

# A cloud sandbox re-signs TLS with its own CA. run.sh copies it into
# scripts/dev/extra-ca/ when HOOPDEV_CA_CERT is set; otherwise the folder holds
# only its README and this step does nothing.
COPY scripts/dev/extra-ca/ /tmp/extra-ca/
RUN if [ -s /tmp/extra-ca/extra-ca.crt ]; then \
    cp /tmp/extra-ca/extra-ca.crt /usr/local/share/ca-certificates/hoopdev-extra-ca.crt && \
    update-ca-certificates; \
  fi && rm -rf /tmp/extra-ca

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
