

database "foo" "bar" {
    engine           = "postgres" # required
    postgres_schemas = ["default"] # default to public

    on_create   = [
        "CREATE ROLE \"${user}\" WITH LOGIN ENCRYPTED PASSWORD '${password}' VALID UNTIL '${expiration}'",
        "GRANT SELECT ON ALL TABLES IN SCHEMA {{ schema }} TO {{ user }}",
        "GRANT USAGE ON SCHEMA {{ schema }} TO {{ user }}"
    ]
    on_existent = [
        "ALTER ROLE \"{{ user }}\" WITH LOGIN ENCRYPTED PASSWORD '{{ password }}' VALID UNTIL '{{ expiration }}'"
    ]
}

database "192.168.15.48" "testdb" {
    engine           = "postgres" # required
    postgres_schemas = ["public", "main"] # default to public

    on_create   = [
        "CREATE ROLE \"${user}\" WITH LOGIN ENCRYPTED PASSWORD '${password}' VALID UNTIL '${expiration}'",
        "GRANT SELECT, UPDATE ON ALL TABLES IN SCHEMA {{ schema }} TO {{ user }}",
        "GRANT USAGE ON SCHEMA {{ schema }} TO {{ user }}"
    ]
    on_existent = [
        "ALTER ROLE \"{{ user }}\" WITH LOGIN ENCRYPTED PASSWORD '{{ password }}' VALID UNTIL '{{ expiration }}'"
    ]
}
