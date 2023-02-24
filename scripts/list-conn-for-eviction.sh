#!/bin/bash -e

: "${1:? Missing connection name argument}"
: "${2:? Missing org id argument}"

CONNECTION_ID=$(curl -s '127.0.0.1:3001/_xtdb/query' \
--header 'Content-Type: application/edn' \
--header 'Accept: application/json' \
--data-raw "{:query {
    :find [conn-id]
    :where [[?c :connection/org \"$2\"]
            [?c :connection/name \"$1\"]
            [?c :xt/id conn-id]]
}}" | jq .[][] -r)


if [[ $CONNECTION_ID == "" ]]; then
  echo "connection '$1' not found" >&2
  exit 2
fi

PLUGIN_CONNECTION_IDS=$(curl -s '127.0.0.1:3001/_xtdb/query' \
--header 'Content-Type: application/edn' \
--header 'Accept: application/json' \
--data-raw "{:query {
    :find [id]
    :where [[?c :plugin-connection/id \"$CONNECTION_ID\"]
            [?c :xt/id id]]
}}" | jq .[][] -r)

cat - <<EOF
{:tx-ops [
    [:xtdb.api/evict
      "$CONNECTION_ID"
        $( for id in $PLUGIN_CONNECTION_IDS; do
echo "      \"$id\""; done)
    ]
]}
EOF

echo -e "\n[PLUGINS]\n---------"
echo -e "In the structure below:
Remove the ids found in the eviction list and update the plugins removing the association with the connections.
After that it's safe to evict the entries above\n--------\n"

curl -s --location --request POST '127.0.0.1:3001/_xtdb/query' \
--header 'Content-Type: application/edn' \
--data-raw "{:query {
    :find [(pull ?p [*])]
    :where [[?p :plugin/org \"$2\"]]
}}"

echo -e ""
