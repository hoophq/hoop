#!/bin/bash

set -eo pipefail

# clear before starting
docker stop hoopdevpg 2&> /dev/null || true
rm -rf $HOME/.hoop/dev/pgdata
mkdir -p $HOME/.hoop/dev/pgdata

echo "--> STARTING POSTGRES DEV SERVER ..."

PGUSER=hoopdevuser
PGDATABASE=hoopdevdb
PGPASSWORD="1a2b3c4d"

docker run -p 5449:5432 -d --rm --name hoopdevpg \
    -e POSTGRES_USER=$PGUSER \
    -e POSTGRES_DB=$PGDATABASE \
    -e POSTGRES_PASSWORD=$PGPASSWORD \
    -e PGUSER=$PGUSER \
    -e PGDATABASE=$PGDATABASE \
    -e PGPASSWORD=$PGPASSWORD \
    -e PG_DATA=/var/lib/postgresql/data/pgdata \
    -v $HOME/.hoop/dev/pgdata:/var/lib/postgresql/data/ \
    postgres:14


until docker exec -it hoopdevpg psql -h 0 --quiet -c 'select now()' -o /dev/null 1> /dev/null
do
    sleep 1
    echo -n '.'
done

echo ""
echo "--> done!"

echo ""
echo "postgres://$PGUSER:$PGPASSWORD@127.0.0.1:5449/$PGDATABASE"
