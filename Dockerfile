FROM ubuntu:focal-20230605

ENV DEBIAN_FRONTEND=noninteractive
ENV ACCEPT_EULA=y
ENV NODE_VERSION 18.17.0
ENV POSTGREST_VERSION 11.2.2

RUN ARCH= && dpkgArch="$(dpkg --print-architecture)" \
    && case "${dpkgArch##*-}" in \
      amd64) ARCH='x64';; \
      arm64) ARCH='arm64';; \
      i386) ARCH='x86';; \
      *) echo "unsupported architecture"; exit 1 ;; \
    esac \
    && set -ex \
    # libatomic1 for arm
    && apt-get update && apt-get install -y \
        ca-certificates \
        curl \
        wget \
        gnupg \
        dirmngr \
        xz-utils \
        libatomic1 \
        --no-install-recommends \
    && rm -rf /var/lib/apt/lists/* \
    && for key in \
      4ED778F539E3634C779C87C6D7062848A1AB005C \
      141F07595B7B3FFE74309A937405533BE57C7D57 \
      74F12602B6F1C4E913FAA37AD3A89613643B6201 \
      DD792F5973C6DE52C432CBDAC77ABFA00DDBF2B7 \
      61FC681DFB92A079F1685E77973F295594EC4689 \
      8FCCA13FEF1D0C2E91008E09770F7A9A5AE15600 \
      C4F0DFFF4E8C1A8236409D08E73BC641CC11F4C8 \
      890C08DB8579162FEE0DF9DB8BEAB4DFCF555EF4 \
      C82FA3AE1CBEDC6BE46B9360C43CEC45C17AB93C \
      108F52B48DB57BB0CC439B2997B01419BD92F80A \
    ; do \
      gpg --batch --keyserver hkps://keys.openpgp.org --recv-keys "$key" || \
      gpg --batch --keyserver keyserver.ubuntu.com --recv-keys "$key" ; \
    done \
    && curl -fsSLO --compressed "https://nodejs.org/dist/v$NODE_VERSION/node-v$NODE_VERSION-linux-$ARCH.tar.xz" \
    && curl -fsSLO --compressed "https://nodejs.org/dist/v$NODE_VERSION/SHASUMS256.txt.asc" \
    && gpg --batch --decrypt --output SHASUMS256.txt SHASUMS256.txt.asc \
    && grep " node-v$NODE_VERSION-linux-$ARCH.tar.xz\$" SHASUMS256.txt | sha256sum -c - \
    && tar -xJf "node-v$NODE_VERSION-linux-$ARCH.tar.xz" -C /usr/local --strip-components=1 --no-same-owner \
    && rm "node-v$NODE_VERSION-linux-$ARCH.tar.xz" SHASUMS256.txt.asc SHASUMS256.txt \
    && apt-mark auto '.*' > /dev/null \
    && find /usr/local -type f -executable -exec ldd '{}' ';' \
      | awk '/=>/ { so = $(NF-1); if (index(so, "/usr/local/") == 1) { next }; gsub("^/(usr/)?", "", so); print so }' \
      | sort -u \
      | xargs -r dpkg-query --search \
      | cut -d: -f1 \
      | sort -u \
      | xargs -r apt-mark manual \
    && apt-get purge -y --auto-remove -o APT::AutoRemove::RecommendsImportant=false \
    && ln -s /usr/local/bin/node /usr/local/bin/nodejs \
    # smoke tests
    && node --version \
    && npm --version

RUN mkdir -p /app && \
    mkdir -p /opt/hoop/sessions && \
    apt-get update -y && \
    apt-get install -y \
        xz-utils \
        locales \
        tini \
        openssh-client \
        procps \
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

RUN mkdir -p /app && \
    mkdir -p /opt/hoop/sessions

RUN sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && \
    locale-gen
ENV LANG en_US.UTF-8
ENV LANGUAGE en_US:en
ENV LC_ALL en_US.UTF-8

COPY rootfs /
COPY dist/webapp-resources /app/ui/
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
