# ENG-880 c4: the session id in a Postgres audit row, through libpq options.
H=${SIDECAR_HOST:-sidecar.eng880.internal}
psql "host=$H port=15432 dbname=appdb user=agent password=agent-demo-password connect_timeout=8 options='-c agentcore.session.id=$1'" -Atc 'select id, name, email from customers order by id' 2>&1
psql "host=$H port=15432 dbname=appdb user=agent password=agent-demo-password connect_timeout=8 options='-c agentcore.session.id=$1'" -Atc "update customers set plan = 'free' where id = 1" 2>&1
echo "sslmode=require through the lane:"; psql "host=$H port=15432 dbname=appdb user=agent password=agent-demo-password connect_timeout=8 sslmode=require" -Atc 'select 1' 2>&1 | tail -1
