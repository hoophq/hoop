#!/bin/bash
# Provisions the HOOP.TEST domain and hands SQL Server its keytab.
#
# Everything here is throwaway: one realm, three accounts, passwords in the
# clear. It exists so the Kerberos lane can be proved end to end on a laptop,
# not to model how anyone should run a domain.
set -euo pipefail

REALM=HOOP.TEST
DOMAIN=HOOP
ADMIN_PASS='Adm1n!Passw0rd'
KEYTAB=/keytabs/mssql.keytab

# The SPN a client derives from the name it dialled. That name resolves to
# ENVOY, while this ticket is only decryptable by the account holding the key
# below — which is the whole reason a relay can sit in the middle unseen.
SQL_SPN="MSSQLSvc/mssql.hoop.test:1433"

# The SAME service, reachable under a second name that bypasses Envoy and the
# relay entirely. mssql-kerberos-check.sh runs the same login down both names
# and compares: that comparison is what distinguishes "hoop-inspect broke
# Kerberos" from "this server would have refused the login anyway", and it
# only works if the KDC will issue a ticket for the direct name too.
DIRECT_SPN="MSSQLSvc/sqlhost.hoop.test:1433"

# Postgres. Note the shape difference from MSSQLSvc: no port, because libpq
# builds krbsrvname/<host> and stops there.
PG_SPN="postgres/appdb.hoop.test"
PG_KEYTAB=/keytabs/postgres.keytab

log() { printf '\033[35m[samba]\033[0m %s\n' "$*"; }

if [[ ! -f /var/lib/samba/private/sam.ldb ]]; then
    log "provisioning $REALM (netbios $DOMAIN)"
    rm -f /etc/samba/smb.conf

    samba-tool domain provision \
        --use-rfc2307 \
        --realm="$REALM" \
        --domain="$DOMAIN" \
        --server-role=dc \
        --dns-backend=SAMBA_INTERNAL \
        --adminpass="$ADMIN_PASS" \
        --option="dns forwarder = 127.0.0.11"

    # Docker's embedded resolver lives at 127.0.0.11 in every container. Samba
    # forwards anything outside the realm there, so a host that points its
    # resolver at this DC can still resolve ordinary compose service names.
    log "domain provisioned"

    # Passwords here are deliberately weak and would be rejected by the
    # default policy, which is tuned for real domains and not for demos.
    samba-tool domain passwordsettings set --complexity=off --min-pwd-length=6 \
        --history-length=0 --max-pwd-age=0 >/dev/null

    log "creating accounts"
    # The human whose ticket travels through the relay.
    samba-tool user create alice 'alicepass' \
        --given-name=Alice --surname=Liddell >/dev/null

    # The SQL Server service account. The SPN attaches to THIS account, so the
    # key that decrypts a client's service ticket is this account's key.
    samba-tool user create mssqlsvc 'Sv c!Passw0rd' >/dev/null
    samba-tool user setexpiry mssqlsvc --noexpiry >/dev/null
    samba-tool spn add "$SQL_SPN" mssqlsvc >/dev/null

    # SQL Server performs its LDAP lookups as this account, so it needs to be
    # able to read the directory. Domain Admins is heavier than necessary and
    # keeps the demo from failing on an ACL detail that teaches nothing.
    samba-tool group addmembers "Domain Admins" mssqlsvc >/dev/null
else
    log "reusing the existing $REALM database"
fi

# Outside the provisioning block and tolerant of "already exists", so a volume
# created before this SPN was introduced picks it up on restart instead of
# silently failing the control test.
samba-tool spn add "$DIRECT_SPN" mssqlsvc >/dev/null 2>&1 \
    && log "registered $DIRECT_SPN" \
    || true

# The Postgres service. libpq builds its SPN as krbsrvname/<host it dialled>,
# with krbsrvname defaulting to "postgres" and NO port component — unlike
# MSSQLSvc/host:port. appdb.hoop.test resolves to Envoy, exactly as
# mssql.hoop.test does, and the ticket stays decryptable only by the database.
samba-tool user create pgsvc 'Pg!Passw0rd' >/dev/null 2>&1 || true
samba-tool user setexpiry pgsvc --noexpiry >/dev/null 2>&1 || true
samba-tool spn add "$PG_SPN" pgsvc >/dev/null 2>&1 \
    && log "registered $PG_SPN" \
    || true

log "exporting keytab for $PG_SPN"
rm -f "$PG_KEYTAB"
samba-tool domain exportkeytab "$PG_KEYTAB" --principal="$PG_SPN" >/dev/null
# The postgres image runs as uid 999, and the server refuses a keytab that
# other accounts can read.
chown 999:0 "$PG_KEYTAB"
chmod 400 "$PG_KEYTAB"

log "exporting keytab for $SQL_SPN"
mkdir -p "$(dirname "$KEYTAB")"
rm -f "$KEYTAB"
# Both principals go in one keytab: the SPN decrypts inbound client tickets,
# and the account principal is what SQL Server authenticates AS when it makes
# its own LDAP queries.
samba-tool domain exportkeytab "$KEYTAB" --principal="$SQL_SPN" >/dev/null
samba-tool domain exportkeytab "$KEYTAB" --principal=mssqlsvc >/dev/null

# SQL Server runs as uid 10001 and refuses a keytab any other account can
# read, exiting rather than starting insecurely.
chown 10001:0 "$KEYTAB"
chmod 400 "$KEYTAB"
log "keytab ready at $KEYTAB (uid 10001, mode 400)"

log "starting samba in the foreground"
exec samba -i --debuglevel=1
