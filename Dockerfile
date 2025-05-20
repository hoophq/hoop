FROM ubuntu:focal-20250404

ENV DEBIAN_FRONTEND=noninteractive
ENV ACCEPT_EULA=y

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
ENV PATH="${PATH}:/opt/hoop/bin"

ENTRYPOINT ["tini", "--"]
