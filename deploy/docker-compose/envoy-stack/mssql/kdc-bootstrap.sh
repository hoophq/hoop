#!/bin/bash
# Builds a throwaway MIT Kerberos realm and hands SQL Server its keytab.
#
# No Active Directory anywhere. SQL Server on Linux authenticates a client by
# decrypting the service ticket with a key from its keytab and then looking
# the principal up in sys.syslogins — a local table. Neither step needs a
# directory, which is why an MIT KDC is enough to prove the relay works.
#
# What DOES need AD is group refresh, so this demo grants logins to principals
# directly rather than through groups. See mssql/README.md.
set -euo pipefail

REALM=HOOP.TEST
KEYTAB_DIR=/keytabs

# The SPN a client computes from the name it dialled. Clients build
# MSSQLSvc/<host>:<port> and that format is hardcoded in every SQL client, so
# this string is the contract between the client's connection target, the
# Envoy alias, and SQL Server's keytab.
SQL_SPN="MSSQLSvc/mssql.hoop.test:1433"

log() { printf '\033[36m[kdc]\033[0m %s\n' "$*"; }

if [[ -f /var/lib/krb5kdc/principal ]]; then
    log "realm $REALM already exists, reusing it"
else
    log "creating realm $REALM"
    # -W pulls entropy from /dev/urandom. Without it kdb5_util blocks on
    # /dev/random inside a container that has generated no entropy yet, and
    # the whole stack hangs on a step that should take a second.
    kdb5_util create -s -r "$REALM" -P masterpassword -W

    log "adding principals"
    # The human. -pw rather than a keytab: the demo runs kinit with a
    # password so the flow matches what a developer actually does.
    kadmin.local -q "addprinc -pw alicepass alice@$REALM"

    # The service. randkey, because nothing should ever type this one.
    kadmin.local -q "addprinc -randkey $SQL_SPN@$REALM"

    # SQL Server also wants a host principal for the machine account it uses
    # when it talks to itself; harmless to have and awkward to add later.
    kadmin.local -q "addprinc -randkey host/mssql.hoop.test@$REALM"
fi

log "exporting keytab for $SQL_SPN"
mkdir -p "$KEYTAB_DIR"
rm -f "$KEYTAB_DIR/mssql.keytab"
kadmin.local -q "ktadd -k $KEYTAB_DIR/mssql.keytab $SQL_SPN@$REALM"
kadmin.local -q "ktadd -k $KEYTAB_DIR/mssql.keytab host/mssql.hoop.test@$REALM"

# The SQL Server image runs as uid 10001 and REFUSES a keytab that any other
# account can read: "The keytab file is not owned by the SQL Server process
# user" and it exits. Setting this here rather than in the SQL container
# avoids a root sidecar whose only job is a chmod.
chown 10001:0 "$KEYTAB_DIR/mssql.keytab"
chmod 400 "$KEYTAB_DIR/mssql.keytab"
log "keytab ready at $KEYTAB_DIR/mssql.keytab (uid 10001, mode 400)"

log "starting krb5kdc in the foreground"
exec /usr/sbin/krb5kdc -n
