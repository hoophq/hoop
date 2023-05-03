FROM ubuntu:focal-20230308

ENV DEBIAN_FRONTEND=noninteractive
ENV ACCEPT_EULA=y

# Common
RUN mkdir -p /app && \
    mkdir -p /opt/hoop/sessions && \
    apt-get update -y && \
    apt-get install -y \
        locales \
        tini \
        apt-utils \
        curl \
        gnupg \
        gnupg2 \
        groff \
        jq \
        openssh-client \
        unzip \
        expect \
        lsb-release

RUN curl --proto '=https' --tlsv1.2 -sSf -L https://install.determinate.systems/nix > nix-installer.sh && \
    chmod +x nix-installer.sh && \
    ./nix-installer.sh install linux --init none --no-confirm && \
    rm -f ./nix-installer.sh && \
    echo > /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh && \
    /root/.nix-profile/bin/nix --version

# kubectl / aws-cli / aws-session-manager
RUN curl -sL "https://dl.k8s.io/release/v1.22.1/bin/linux/amd64/kubectl" -o kubectl && \
        echo '78178a8337fc6c76780f60541fca7199f0f1a2e9c41806bded280a4a5ef665c9  kubectl' | sha256sum -c --ignore-missing --strict - && \
        chmod 755 kubectl && \
        mv kubectl /usr/local/bin/kubectl

RUN echo "deb http://apt.postgresql.org/pub/repos/apt/ focal-pgdg main" | tee /etc/apt/sources.list.d/pgdg.list && \
    echo "deb [arch=amd64,arm64] https://repo.mongodb.org/apt/ubuntu focal/mongodb-org/5.0 multiverse" | tee /etc/apt/sources.list.d/mongodb-org-5.0.list && \
    echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" | tee -a /etc/apt/sources.list.d/google-cloud-sdk.list && \
    # echo "deb https://cli-assets.heroku.com/apt ./" > /etc/apt/sources.list.d/heroku.list && \
    # curl -sL https://cli-assets.heroku.com/apt/release.key | apt-key add - && \
    curl -sL https://packages.microsoft.com/config/ubuntu/20.04/prod.list | tee /etc/apt/sources.list.d/msprod.list && \
    curl -sL https://www.postgresql.org/media/keys/ACCC4CF8.asc | apt-key add - && \
    curl -sL https://www.mongodb.org/static/pgp/server-5.0.asc | apt-key add - && \
    curl -sL https://packages.microsoft.com/keys/microsoft.asc | apt-key add - && \
    curl -sL https://packages.cloud.google.com/apt/doc/apt-key.gpg | apt-key --keyring /usr/share/keyrings/cloud.google.gpg add -

RUN apt-get update -y && \
    apt-get install -y \
        mongodb-mongosh mongodb-org-tools mongodb-org-shell \
        openjdk-11-jre \
        # heroku \
        default-mysql-client \
        postgresql-client-15 \
        google-cloud-cli=416.0.0-0 && \
        # mssql-tools unixodbc-dev && \
        rm -rf /var/lib/apt/lists/*

RUN curl -sL https://hoopartifacts.s3.amazonaws.com/xtdb-in-memory-1.22.0-aarch64.tar.gz -o /app/xtdb-in-memory-1.22.0-aarch64.tar.gz && \
    curl -sL https://hoopartifacts.s3.amazonaws.com/xtdb-in-memory-1.22.0-x86_64.tar.gz -o /app/xtdb-in-memory-1.22.0-x86_64.tar.gz && \
    tar -xf /app/xtdb-in-memory-1.22.0-x86_64.tar.gz && \
    tar -xf /app/xtdb-in-memory-1.22.0-aarch64.tar.gz && \
    mv xtdb-in-memory-1.22.0-aarch64 /app/ && \
    mv xtdb-in-memory-1.22.0-x86_64 /app/ && \
    rm -f /app/*.tar.gz

RUN curl -sL https://github.com/42wim/matterbridge/releases/download/v1.26.0/matterbridge-1.26.0-linux-64bit -o /usr/local/bin/matterbridge && \
    chmod +x /usr/local/bin/matterbridge && \
    matterbridge -version

RUN sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && \
    locale-gen
ENV LANG en_US.UTF-8
ENV LANGUAGE en_US:en  
ENV LC_ALL en_US.UTF-8

ENV NIX_SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV PATH="/app:/root/.nix-profile/bin:/nix/var/nix/profiles/default/bin:$PATH:/opt/mssql-tools/bin"

COPY rootfs /
COPY hoop* /app/

EXPOSE 8009
EXPOSE 8010

ENTRYPOINT ["tini", "--"]
