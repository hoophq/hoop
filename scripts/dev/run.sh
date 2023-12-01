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
: "${NODE_API_URL:=http://localhost:4001}"


trap ctrl_c INT

function ctrl_c() {
    docker stop hoopdev && docker rm hoopdev
    exit 130
}

mkdir -p $HOME/.hoop/dev

# Dockerfile with agent tools
cp ./scripts/dev/Dockerfile $HOME/.hoop/dev/Dockerfile

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
 :xtdb.rocksdb/block-cache {:xtdb/module xtdb.rocksdb/->lru-block-cache
                            :cache-size 536870912}
 :xtdb/index-store
 {:kv-store {:xtdb/module xtdb.rocksdb/->kv-store
             :block-cache :xtdb.rocksdb/block-cache
             :db-dir "/opt/hoop/sessions/rocksdb"
             :sync? false}}
 :xtdb/tx-log
 {:xtdb/module xtdb.jdbc/->tx-log,
  :connection-pool :xtdb.jdbc/connection-pool},
 :xtdb/document-store
 {:xtdb/module xtdb.jdbc/->document-store,
  :connection-pool :xtdb.jdbc/connection-pool}
 :xtdb.http-server/server {:port 3001
                           :jetty-opts {:host "0.0.0.0"}}}
EOF

cp ./scripts/dev/entrypoint.sh $HOME/.hoop/dev/entrypoint.sh
rm -rf $HOME/.hoop/dev/migrations && \
  cp -a ./rootfs/app/migrations $HOME/.hoop/dev/migrations

chmod +x $HOME/.hoop/dev/entrypoint.sh
docker build -t hoopdev -f $HOME/.hoop/dev/Dockerfile $HOME/.hoop/dev/

GOOS=linux go build -ldflags "-s -w -X github.com/runopsio/hoop/common/version.strictTLS=false" -o $HOME/.hoop/dev/hooplinux github.com/runopsio/hoop/client
docker stop hoopdev > /dev/null || true
docker run --name hoopdev \
  -p 3001:3001 \
  -p 8008:8008 \
  -p 8009:8009 \
  -p 8010:8010 \
  --env-file=.env \
  -v $HOME/.hoop/dev:/app/ \
  -v $HOME/.hoop/dev/webapp/resources:/app/ui/ \
  -v $HOME/.hoop/dev/sessions:/opt/hoop/sessions/ \
  --rm -it hoopdev /app/entrypoint.sh
