#!/bin/bash

set -eo pipefail

if ! [[ -f .env ]]; then
  echo "missing .env file"
  exit 1
fi

set -o allexport
source .env
set +o allexport

: "${PG_HOST:?Variable not set or empty}"
: "${PG_DB:?Variable not set or empty}"
: "${PG_USER:?Variable not set or empty}"
: "${PG_PASSWORD:?Variable not set or empty}"
: "${PG_PORT:=5432}"

: "${IDP_CLIENT_ID:?Variable not set or empty}"
: "${IDP_CLIENT_SECRET:?Variable not set or empty}"
: "${IDP_ISSUER:?Variable not set or empty}"
: "${IDP_AUDIENCE:?Variable not set or empty}"

: "${API_URL:=http://localhost:8009}"
: "${GRPC_URL:=http://127.0.0.1:8010}"
: "${ORG_MULTI_TENANT:=false}"
: "${GIN_MODE:=release}"
: "${AUTO_REGISTER:=1}"
: "${PORT:=8009}"
: "${XTDB_ADDRESS:=http://127.0.0.1:3001}"
: "${LOG_LEVEL:=info}"
: "${LOG_ENCODING:=console}"
: "${ADMIN_USERNAME:=admin}"
: "${PLUGIN_REGISTRY_URL:=https://pluginregistry.s3.amazonaws.com/packages.json}"

trap ctrl_c INT

function ctrl_c() {
    docker stop hoopdev && docker rm hoopdev
    exit 130
}

mkdir -p $HOME/.hoop/dev

# Dockerfile with agent tools
cat - > $HOME/.hoop/dev/Dockerfile <<EOF
FROM ubuntu:focal-20230605

ENV DEBIAN_FRONTEND=noninteractive
ENV ACCEPT_EULA=y

RUN mkdir -p /app && \
    mkdir -p /opt/hoop/sessions && \
    apt-get update -y && \
    apt-get install -y \
        locales \
        tini \
        openssh-client \
        apt-utils \
        curl \
        gnupg \
        gnupg2

RUN echo "deb http://apt.postgresql.org/pub/repos/apt/ focal-pgdg main" | tee /etc/apt/sources.list.d/pgdg.list && \
    echo "deb [arch=amd64,arm64] https://repo.mongodb.org/apt/ubuntu focal/mongodb-org/5.0 multiverse" | tee /etc/apt/sources.list.d/mongodb-org-5.0.list && \
    curl -sL https://www.postgresql.org/media/keys/ACCC4CF8.asc | apt-key add - && \
    curl -sL https://www.mongodb.org/static/pgp/server-5.0.asc | apt-key add -

RUN apt-get update -y && \
    apt-get install -y \
        openjdk-11-jre \
        default-mysql-client \
        postgresql-client-15 \
        mongodb-mongosh mongodb-org-tools mongodb-org-shell mongocli \
        nodejs


RUN sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && \
    locale-gen
ENV LANG en_US.UTF-8
ENV LANGUAGE en_US:en 
ENV LC_ALL en_US.UTF-8

ENV PATH="/app:${PATH}"

ENTRYPOINT ["tini", "--"]

EOF


cat - > $HOME/.hoop/dev/logback.xml <<EOF
<configuration>
  <appender name="STDOUT" class="ch.qos.logback.core.ConsoleAppender">
    <encoder>
      <pattern>%d{HH:mm:ss.SSS} [%thread] %-5level %logger{36} - %msg%n</pattern>
    </encoder>
  </appender>
  <root level="INFO">
    <appender-ref ref="STDOUT" />
  </root>

  <logger name="org.apache.kafka" level="ERROR" />
  <logger name="org.apache.zookeeper" level="ERROR" />
  <logger name="kafka" level="ERROR" />
</configuration>
EOF

cat - > $HOME/.hoop/dev/xtdb.edn <<EOF
{:xtdb.jdbc/connection-pool
 {:dialect #:xtdb{:module xtdb.jdbc.psql/->dialect},
  :db-spec
  {:host "$PG_HOST",
   :dbname "$PG_DB",
   :user "$PG_USER",
   :password "$PG_PASSWORD",
   :port $PG_PORT}},
 :xtdb/tx-log
 {:xtdb/module xtdb.jdbc/->tx-log,
  :connection-pool :xtdb.jdbc/connection-pool},
 :xtdb/document-store
 {:xtdb/module xtdb.jdbc/->document-store,
  :connection-pool :xtdb.jdbc/connection-pool}
 :xtdb.http-server/server {:port 3001
                           :jetty-opts {:host "0.0.0.0"}}}
EOF

cat - > $HOME/.hoop/dev/entrypoint.sh <<EOF
#!/bin/bash

cd /app/
java -Dlogback.configurationFile=/app/logback.xml -jar /app/xtdb-pg.jar &
echo "--> STARTING GATEWAY ..."

/app/hooplinux start gateway --listen-admin-addr "0.0.0.0:8099" &

until curl -s -f -o /dev/null "http://127.0.0.1:8009/api/healthz"
do
  sleep 1
done
echo "done"
echo "--> STARTING AGENT ..."

curl -s -f -o /dev/null "http://127.0.0.1:3001/_xtdb/status" || exit 137
AUTO_REGISTER=1 /app/hooplinux start agent &

sleep infinity
EOF

chmod +x $HOME/.hoop/dev/entrypoint.sh

docker build -t hoopdev -f $HOME/.hoop/dev/Dockerfile $HOME/.hoop/dev/

GOOS=linux go build -ldflags "-s -w" -o $HOME/.hoop/dev/hooplinux github.com/runopsio/hoop/client
docker stop hoopdev > /dev/null || true
docker run --name hoopdev \
  -p 3001:3001 \
  -p 8009:8009 \
  -p 8010:8010 \
  -p 8099:8099 \
  --env-file=.env \
  -v $HOME/.hoop/dev:/app/ \
  -v $HOME/.hoop/dev/webapp/resources:/app/ui/ \
  --rm -it hoopdev /app/entrypoint.sh
