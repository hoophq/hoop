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
        curl

RUN curl -sL https://github.com/42wim/matterbridge/releases/download/v1.26.0/matterbridge-1.26.0-linux-64bit -o /usr/local/bin/matterbridge && \
    chmod +x /usr/local/bin/matterbridge && \
    matterbridge -version

RUN sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && \
    locale-gen
ENV LANG en_US.UTF-8
ENV LANGUAGE en_US:en  
ENV LC_ALL en_US.UTF-8

COPY rootfs /
COPY dist/webapp-resources /app/ui/
COPY dist/binaries/ /tmp/
RUN tar -xf /tmp/binaries/$(uname -s)_$(dpkg --print-architecture)/hoop_*_$(uname -s)_$(dpkg --print-architecture).tar.gz -C /app/ && \
    rm -rf /tmp/*

EXPOSE 8009
EXPOSE 8010

ENTRYPOINT ["tini", "--"]
