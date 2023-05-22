// hoop admin create plugin dcm \
// 	--config 192.168.15.48:5444/testdb='postgres://_hoop_role_granter:123@{{address}}/{{database}}?sslmode=disable' \
// 	--config 192.168.15.48:5444/dellstore='postgres://_hoop_role_granter:123@{{address}}/{{database}}?sslmode=disable' \
// 	--overwrite

// hoop admin create conn pg-db-prod \
// 	--overwrite \
// 	--agent default \
// 	--type postgres \
// 	--plugin 'dcm:db-prod.foo.bar.com'

// hoop admin create conn pg-db-main \
// 	--overwrite \
// 	--agent default \
// 	--type postgres \
// 	--plugin 'dcm:db-prod.foo.bar.com'


database "*/*" {
    engine           = "postgres" # required
    expiration       = "10h"
    postgres_schemas = ["public"] # default to public

	on_create   = [
        "CREATE ROLE \"{{ user }}\" WITH LOGIN ENCRYPTED PASSWORD '{{ password }}' VALID UNTIL '{{ expiration }}'"
        "GRANT SELECT ON ALL TABLES IN SCHEMA {{ schema }} TO {{ user }}"
        "GRANT USAGE ON SCHEMA {{ schema }} TO {{ user }}"
    ]
    on_existent = [
        "ALTER ROLE \"{{ user }}\" WITH LOGIN ENCRYPTED PASSWORD '{{ password }}' VALID UNTIL '{{ expiration }}'"
    ]
}

database "192.168.15.48/testdb" {
    engine           = "postgres" # required
    expiration       = "10h"
    postgres_schemas = ["public", "main"] # default to public

    on_create   = [
        "CREATE ROLE \"{{ user }}\" WITH LOGIN ENCRYPTED PASSWORD '{{ password }}' VALID UNTIL '{{ expiration }}'"
        "GRANT SELECT, UPDATE ON ALL TABLES IN SCHEMA {{ schema }} TO {{ user }}"
        "GRANT USAGE ON SCHEMA {{ schema }} TO {{ user }}"
    ]
    on_existent = [
        "ALTER ROLE \"{{ user }}\" WITH LOGIN ENCRYPTED PASSWORD '{{ password }}' VALID UNTIL '{{ expiration }}'"
    ]
}
