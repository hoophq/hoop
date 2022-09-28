FROM ubuntu:focal-20220801

ENV DEBIAN_FRONTEND=noninteractive
ENV ACCEPT_EULA=y

ADD rootfs/tmp/* /tmp/

# Common
RUN mkdir -p /app && apt-get update -y && \
    apt-get install -y \
        locales \
        tini \
        apt-utils \
        curl \
        gnupg \
        gnupg2 \
        groff \
        unzip \
        expect \
        lsb-release

# 
# kubectl / aws-cli / aws-session-manager
RUN curl -sL "https://dl.k8s.io/release/v1.22.1/bin/linux/amd64/kubectl" -o kubectl && \
        sha256sum -c /tmp/checksum-kubectl.txt --ignore-missing --strict && \
        chmod 755 kubectl && \
        mv kubectl /usr/local/bin/kubectl
    # curl -sL "https://s3.amazonaws.com/session-manager-downloads/plugin/1.2.245.0/ubuntu_64bit/session-manager-plugin.deb" -o session-manager-plugin.deb && \
    #     sha256sum -c /tmp/checksum-aws-sess-manager-plugin.txt --ignore-missing --strict && \
    #     dpkg -i session-manager-plugin.deb && \
    #     rm -f /tmp/* session-manager-plugin.deb

RUN echo "deb http://apt.postgresql.org/pub/repos/apt/ focal-pgdg main" | tee /etc/apt/sources.list.d/pgdg.list && \
    echo "deb [arch=amd64,arm64] https://repo.mongodb.org/apt/ubuntu focal/mongodb-org/5.0 multiverse" | tee /etc/apt/sources.list.d/mongodb-org-5.0.list && \
    # echo "deb https://cli-assets.heroku.com/apt ./" > /etc/apt/sources.list.d/heroku.list && \
    # curl -sL https://cli-assets.heroku.com/apt/release.key | apt-key add - && \
    curl -sL https://packages.microsoft.com/config/ubuntu/20.04/prod.list | tee /etc/apt/sources.list.d/msprod.list && \
    curl -sL https://www.postgresql.org/media/keys/ACCC4CF8.asc | apt-key add - && \
    curl -sL https://www.mongodb.org/static/pgp/server-5.0.asc | apt-key add - && \
    curl -sL https://packages.microsoft.com/keys/microsoft.asc | apt-key add -

RUN apt-get update -y && \
    apt-get install -y \
        mongodb-mongosh mongodb-org-tools mongodb-org-shell \
        awscli \
        openjdk-11-jre \
        # heroku \
        default-mysql-client \
        postgresql-client-13 && \
        # mssql-tools unixodbc-dev && \
        rm -rf /var/lib/apt/lists/*

RUN curl -sL https://hoopartifacts.s3.amazonaws.com/xtdb-in-memory-1.22.0-aarch64.tar.gz -o /app/xtdb-in-memory-1.22.0-aarch64.tar.gz && \
    curl -sL https://hoopartifacts.s3.amazonaws.com/xtdb-in-memory-1.22.0-x86_64.tar.gz -o /app/xtdb-in-memory-1.22.0-x86_64.tar.gz && \
    tar -xf /app/xtdb-in-memory-1.22.0-x86_64.tar.gz && \
    tar -xf /app/xtdb-in-memory-1.22.0-aarch64.tar.gz && \
    mv xtdb-in-memory-1.22.0-aarch64 /app/ && \
    mv xtdb-in-memory-1.22.0-x86_64 /app/ && \
    rm -f /app/*.tar.gz

RUN sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && \
    locale-gen
ENV LANG en_US.UTF-8
ENV LANGUAGE en_US:en  
ENV LC_ALL en_US.UTF-8

ENV PATH="/opt/mssql-tools/bin:/app:${PATH}"

ADD rootfs/app/start.sh /app/
COPY rootfs/ui /app/ui
COPY hoop* /app/
RUN chmod +x /app/*

EXPOSE 8080
EXPOSE 9090

ENTRYPOINT ["tini", "--"]
CMD ["/app/start.sh"]
