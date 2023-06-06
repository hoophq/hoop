# Development

In order to run hoop on Linux with authentication it's required a postgres instance running in your host machine.

- User: hoopapp
- Password: 1a2b3c4d
- Port: 5432

Connect to your postgres instance and create the `hoopapp` role.

```sh
psql -h 0 postgres <<EOF
create role "hoopapp" with login encrypted password '1a2b3c4d' superuser;
EOF
```

## Authentication

To run hoop gateway and an agent with authentication follow the steps below


1. Copy the script file to the hoop path bin

```sh
cp ./scripts/dev/run-all.sh $HOME/.hoop/bin/
```

2. Clone the xtdb repository and build the jar

```sh
git clone git@github.com:hoophq/xtdb.git && cd xtdb
mkdir -p $HOME/.hoop/bin
clojure -T:uberjar :uber-file '"'xtdb-pg-1.23.2.jar'"'

mv xtdb-pg-1.23.2.jar $HOME/.hoop/bin/
cd ../
```

Then, every time you need to test it, just run the script below

```sh
PG_PORT=5444 PG_HOST=192.168.15.48 ./scripts/dev/run-with-auth.sh
```
