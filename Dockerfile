FROM ubuntu:focal-20250404

ENV DEBIAN_FRONTEND=noninteractive
ENV ACCEPT_EULA=y
ENV POSTGREST_VERSION=11.2.2

RUN mkdir -p /app && \
    mkdir -p /opt/hoop/sessions && \
    mkdir -p /opt/hoop/bin && \
    apt-get update -y && \
    apt-get install -y \
        xz-utils \
        locales \
        tini \
        openssh-client \
        procps \
        gettext-base \
        curl

RUN URL= && dpkgArch="$(dpkg --print-architecture)" \
    && case "${dpkgArch##*-}" in \
      amd64) URL="https://github.com/PostgREST/postgrest/releases/download/v$POSTGREST_VERSION/postgrest-v$POSTGREST_VERSION-linux-static-x64.tar.xz";; \
      arm64) URL="https://github.com/PostgREST/postgrest/releases/download/v$POSTGREST_VERSION/postgrest-v$POSTGREST_VERSION-ubuntu-aarch64.tar.xz";; \
      *) echo "unsupported architecture"; exit 1 ;; \
    esac \
    && curl -sL $URL -o postgrest.tar.xz && \
    tar -xf postgrest.tar.xz && rm -f postgrest.tar.xz && \
    mv postgrest /usr/local/bin/postgrest && \
    chmod 0755 /usr/local/bin/postgrest && \
    postgrest --version

RUN sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && \
    locale-gen
ENV LANG=en_US.UTF-8
ENV LANGUAGE=en_US:en
ENV LC_ALL=en_US.UTF-8

COPY rootfs /
COPY dist/binaries/ /tmp/
RUN tar -xf /tmp/hoop_*_$(uname -s)_$(uname -m).tar.gz -C /app/ && \
    chown root:root /app/hoop && \
    chmod 755 /app/hoop && \
    rm -rf /tmp/* && \
    rm -rf /var/cache/apt/archives /var/lib/apt/lists/*

EXPOSE 8009
EXPOSE 8010

ENV PATH="/app:${PATH}"

ENTRYPOINT ["tini", "--"]
