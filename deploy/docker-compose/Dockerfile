FROM nginx:1.27.0

ARG AWS_CLI_VERSION=2.9.6
ARG AWS_SM_VERSION=1.2.398.0
ARG GCLOUD_VERSION=488.0.0-0

RUN apt-get update -y && \
    apt-get install -y \
    python3-dev \
    python3-pip \
    locales \
    tini \
    apt-utils \
    curl \
    gnupg \
    gnupg2 \
    net-tools \
    groff \
    jq \
    openssh-client \
    unzip \
    expect \
    less \
    gettext-base \
    lsb-release && \
    ln -s /usr/bin/python3 /usr/bin/python

# kubectl / aws-cli / aws-session-manager
RUN curl -sL "https://dl.k8s.io/release/v1.22.1/bin/linux/$(dpkg --print-architecture)/kubectl" -o kubectl && \
    chmod 755 kubectl && \
    mv kubectl /usr/local/bin/kubectl

RUN curl -sL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m)-$AWS_CLI_VERSION.zip" -o awscli-exe-linux-$(uname -m)-$AWS_CLI_VERSION.zip && \
    unzip -q awscli-exe-linux-$(uname -m)-$AWS_CLI_VERSION.zip && \
    aws/install && \
    aws --version && \
    rm -rf aws

RUN ARCH= && dpkgArch="$(dpkg --print-architecture)" \
    && case "${dpkgArch##*-}" in \
    amd64) ARCH='64bit';; \
    arm64) ARCH='arm64';; \
    *) echo "unsupported architecture"; exit 1 ;; \
    esac && \
    curl -sL "https://s3.amazonaws.com/session-manager-downloads/plugin/$AWS_SM_VERSION/ubuntu_$ARCH/session-manager-plugin.deb" -o session-manager-plugin.deb && \
    dpkg -i session-manager-plugin.deb && \
    rm -rf /tmp/* session-manager-plugin.deb

RUN curl https://apt.releases.hashicorp.com/gpg -o - | gpg --dearmor | tee /usr/share/keyrings/hashicorp-archive-keyring.gpg > /dev/null && \
    gpg --no-default-keyring --keyring /usr/share/keyrings/hashicorp-archive-keyring.gpg --fingerprint && \
    echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" | tee /etc/apt/sources.list.d/hashicorp.list && \
    echo "deb [arch=amd64,arm64] https://repo.mongodb.org/apt/ubuntu focal/mongodb-org/5.0 multiverse" | tee /etc/apt/sources.list.d/mongodb-org-5.0.list && \
    echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" | tee -a /etc/apt/sources.list.d/google-cloud-sdk.list && \
    curl -sL https://packages.microsoft.com/config/ubuntu/20.04/prod.list | tee /etc/apt/sources.list.d/msprod.list && \
    curl -sL https://www.mongodb.org/static/pgp/server-5.0.asc | apt-key add - && \
    curl -sL https://packages.microsoft.com/keys/microsoft.asc | apt-key add - && \
    curl -sL https://packages.cloud.google.com/apt/doc/apt-key.gpg | apt-key --keyring /usr/share/keyrings/cloud.google.gpg add -

RUN apt-get update -y && \
    apt-get install -y \
    terraform \
    mongodb-mongosh mongodb-org-tools \
    default-mysql-client \
    postgresql-client \
    sqlcmd \
    unixodbc-dev \
    google-cloud-cli=$GCLOUD_VERSION \
    google-cloud-sdk-gke-gcloud-auth-plugin=$GCLOUD_VERSION && \
    rm -rf /var/lib/apt/lists/* && \
    ln -s /usr/bin/mongosh /usr/bin/mongo

RUN sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && \
    locale-gen
ENV LANG en_US.UTF-8
ENV LANGUAGE en_US:en
ENV LC_ALL en_US.UTF-8

COPY rootfs /
COPY dist/hoopgateway_* /tmp/
RUN tar --extract --file /tmp/hoopgateway_*-Linux_$(dpkg --print-architecture).tar.gz -C / --strip 1 && rm -rf /tmp/*

ENTRYPOINT ["tini", "--"]
