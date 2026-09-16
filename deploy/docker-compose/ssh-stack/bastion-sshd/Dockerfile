# A REAL OpenSSH sshd, acting as the bastion an organization already runs.
#
# The point of this container is that it is NOT ours. It is stock sshd with
# two lines of configuration: trust the same CA, and allow forwarding. It
# does not know hoop exists.
#
# That is ADR-0015's mode 2. A client jumps through it to the end-hop, and
# the end-hop still verifies the ORIGINAL client certificate — sshd forwards
# a pipe and never re-signs identity, so nothing about the trust decision
# moves onto a host we do not control.

FROM alpine:3.20
RUN apk add --no-cache openssh-server

# The principal the client presents when jumping. It has to exist as an
# account here, because this is a real sshd and that is how sshd works.
#
# The second line is not decoration. `adduser -D` leaves `!` in the shadow
# password field, and sshd reads that as a LOCKED account and refuses the
# login before it ever looks at the certificate — "User jump not allowed
# because account is locked", which reads like a key problem and is not.
# `*` means "no password login", which is what a certificate-only account
# actually wants.
RUN adduser -D -u 10002 -s /bin/sh jump \
    && sed -i 's/^jump:!/jump:*/' /etc/shadow

RUN ssh-keygen -A
COPY sshd_config /etc/ssh/sshd_config

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
