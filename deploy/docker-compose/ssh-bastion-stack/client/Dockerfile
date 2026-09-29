# Where the walkthrough is typed.
#
# An ORDINARY OpenSSH client. Nothing here is hoop-aware: the whole client
# cost of this topology is a wildcardable ssh_config stanza, and it is in
# ssh_config beside this file where you can read it.

FROM alpine:3.20
RUN apk add --no-cache openssh-client postgresql-client socat

RUN adduser -D -u 10003 -s /bin/sh rider \
    && mkdir -p /home/rider/.ssh \
    && chmod 700 /home/rider/.ssh \
    && chown -R rider:rider /home/rider

USER rider
WORKDIR /home/rider
# Kept alive so the walkthrough can `docker compose exec` into it.
CMD ["sleep", "infinity"]
