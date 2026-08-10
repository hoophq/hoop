# A minimal Active Directory domain controller.
#
# The MIT KDC this replaced issued perfectly valid tickets, and SQL Server
# still refused every login with error 18452, "the login is from an untrusted
# domain". That error is not about Kerberos. Before SQL Server will accept a
# principal it has to RESOLVE it — turn DOMAIN\user into a SID — and that is
# an LDAP question no bare KDC can answer.
#
# Samba provisioned as a DC serves both roles: the KDC that issues the ticket
# and the AD-shaped directory that names the person inside it.
FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive

# Two packaging details, both of which stop the build cold if missed:
#   krb5-config prompts for a default realm unless it is preseeded.
#   samba-ad-provision carries the AD schema .ldf files; without it
#   `samba-tool domain provision` dies on a missing
#   AD_DS_Attributes__Windows_Server_2012_R2.ldf.
RUN echo "krb5-config krb5-config/default_realm string HOOP.TEST" | debconf-set-selections \
    && apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends \
        samba samba-dsdb-modules samba-vfs-modules samba-ad-provision winbind \
        krb5-user krb5-config ldb-tools dnsutils procps \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

COPY provision.sh /usr/local/bin/provision.sh
RUN chmod +x /usr/local/bin/provision.sh

EXPOSE 53/tcp 53/udp 88/tcp 88/udp 389/tcp 636/tcp

ENTRYPOINT ["/usr/local/bin/provision.sh"]
