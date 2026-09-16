# Where every command in the walkthrough is typed.
#
# It carries the four clients the stack demonstrates, because each one takes
# a DIFFERENT route through an SSH connection and therefore produces
# different statements:
#
#   ssh     exec_line, or a shell with no statements at all
#   sftp    one statement per file operation
#   scp     sftp by default on OpenSSH 9; exec_line with -O
#   rsync   exec_line, always — it runs `rsync --server` on the far side
FROM alpine:3.20
RUN apk add --no-cache openssh-client rsync
RUN adduser -D -u 10003 -s /bin/sh rider
USER rider
WORKDIR /home/rider
ENTRYPOINT ["sleep", "infinity"]
