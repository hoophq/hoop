# A STOCK sshd. This is the point of the whole stack: these containers have
# no hoop component, no agent, no sidecar and no configuration hoop wrote.
# They are what an appliance, a managed instance or a host under change-freeze
# looks like — something that takes configuration but not binaries.
#
# What they DO have is exactly what any host already has: accounts, an
# authorized_keys file, and in one case TrustedUserCAKeys. Nothing else is
# added for hoop's benefit.

FROM alpine:3.20
RUN apk add --no-cache openssh-server postgresql-client

# Two accounts, because the fleet has two conventions. `devuser` is the
# ordinary one; `ec2-user` is what the legacy host calls the same person, and
# it exists to be reached through a per-target `login` override rather than by
# the client typing a different name.
#
# The shadow line matters. `adduser -D` leaves `!` in the password field and
# sshd reads that as a LOCKED account, refusing the login before it ever looks
# at a key — "account is locked", which reads like a key problem and is not.
# `*` means "no password login", which is what a certificate-only or
# key-only account actually wants.
RUN adduser -D -u 10001 -s /bin/sh devuser \
    && adduser -D -u 10002 -s /bin/sh ec2-user \
    && sed -i 's/^devuser:!/devuser:*/;s/^ec2-user:!/ec2-user:*/' /etc/shadow \
    && mkdir -p /home/devuser/.ssh /home/ec2-user/.ssh \
    && chmod 700 /home/devuser/.ssh /home/ec2-user/.ssh \
    && chown devuser:devuser /home/devuser/.ssh \
    && chown ec2-user:ec2-user /home/ec2-user/.ssh

# Something for the walkthrough to read, and something it must not.
RUN printf 'ok\n' > /home/devuser/README \
    && printf 'DB_PASSWORD=hunter2\nOWNER=alice@example.com\n' > /home/devuser/secrets.env \
    && printf 'contact: alice@example.com\n' > /home/devuser/team.txt \
    && chown devuser:devuser /home/devuser/README /home/devuser/secrets.env /home/devuser/team.txt

# The host key is MOUNTED, not generated here. run.sh makes it on the host so
# the bastion's known_hosts can name it before anything starts — which is what
# lets host_key_check: strict be the default rather than a thing you turn on
# after the first connection.
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e", "-f", "/etc/ssh/sshd_config"]
