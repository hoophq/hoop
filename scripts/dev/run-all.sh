#!/bin/bash
: "${PG_HOST:?Variable not set or empty}"
: "${PG_DB:?Variable not set or empty}"
: "${PG_USER:?Variable not set or empty}"
: "${PG_PASSWORD:?Variable not set or empty}"
: "${PG_PORT:=5432}"


echo "--> STARTING XTDB..."
cat - > /logback.xml <<EOF
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

cat - > xtdb.edn <<EOF
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

mkdir -p /app/bin

java -Dlogback.configurationFile=/logback.xml -jar /app/bin/xtdb-pg-1.23.2.jar &
echo "--> STARTING GATEWAY ..."

/app/bin/hooplinux start gateway &

until curl -s -f -o /dev/null "http://127.0.0.1:8009/api/healthz"
do
    sleep 1
done
echo "done"
echo "--> STARTING AGENT ..."

curl -s -f -o /dev/null "http://127.0.0.1:3001/_xtdb/status" || exit 137
AUTO_REGISTER=1 /app/bin/hooplinux start agent &

sleep infinity
