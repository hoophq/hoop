#!/usr/bin/env bash
#
# Bring the terminating-bastion stack up, generating everything it needs.
#
# The key material is the interesting part, because it is where this
# topology's three credential sources become concrete:
#
#   keys/prod              ONE key the bastion holds, in host-a's
#                          authorized_keys. host-a's auth.log says "an
#                          account", and hoop's trail is the only place the
#                          person appears.
#   identities/<subject>   ONE KEY PER PERSON, named by certificate subject.
#                          host-b sees a distinct public key per human.
#   no key at all          host-c trusts the user CA. The client's agent
#                          signs, and host-c's auth.log names the person.
#
# And keys/known_hosts, which is generated BEFORE anything starts. That is
# what lets host_key_check stay `strict` by default rather than being
# something you relax to get going.
#
#   ./run.sh            build, generate, start, and print what validate says
#   ./run.sh down       stop and delete the generated material
#   ./run.sh validate   re-run the sidecar's own -validate pass
#   ./run.sh logs       follow the bastion
#   ./run.sh shell      a shell on the client container

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
c_info() { printf '  \033[2m%s\033[0m\n' "$*"; }
h()      { printf '\n\033[1;36m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mrun.sh: %s\033[0m\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }
need docker; need ssh-keygen

COMPOSE=(docker compose)

case "${1:-up}" in
    down)
        "${COMPOSE[@]}" down -v --remove-orphans
        rm -rf keys identities state
        c_ok "stopped, and removed the generated keys"
        exit 0
        ;;
    logs)
        exec "${COMPOSE[@]}" logs -f bastion
        ;;
    shell)
        exec "${COMPOSE[@]}" exec client sh
        ;;
    validate)
        # -validate resolves everything and reports it WITHOUT binding, which
        # is the pass that tells you what this listener will do before anyone
        # can use it.
        exec "${COMPOSE[@]}" run --rm --no-deps --entrypoint /usr/local/bin/hoop-inspect \
            bastion --config /etc/hoop-inspect/config.yaml --validate
        ;;
    up) ;;
    *) die "unknown command ${1}" ;;
esac

# --------------------------------------------------------------------- keys

h "Key material"

mkdir -p keys/probe identities state
chmod 700 identities

if [[ ! -f keys/ca ]]; then
    # The CA, the bastion's own host key, and one host key per target. The
    # target host keys are generated HERE rather than inside the containers
    # so known_hosts can name them before anything starts.
    ssh-keygen -t ed25519 -f keys/ca              -N '' -q -C hoop-demo-ca
    ssh-keygen -t ed25519 -f keys/bastion_host_key -N '' -q -C bastion
    for t in host-a host-b host-c legacy blind; do
        ssh-keygen -t ed25519 -f "keys/${t}_host_key" -N '' -q -C "$t"
    done

    # The bastion's OWN credentials: one shared key for the fleet glob, one
    # for the legacy host.
    ssh-keygen -t ed25519 -f keys/prod   -N '' -q -C bastion-to-prod
    ssh-keygen -t ed25519 -f keys/legacy -N '' -q -C bastion-to-legacy

    # The user's key, and the identity key enrolled for that same subject.
    # They are DIFFERENT keys on purpose: the one the person authenticates to
    # the bastion with never leaves their machine, and the one that reaches
    # host-b never leaves the bastion.
    ssh-keygen -t ed25519 -f keys/user -N '' -q -C rider
    ssh-keygen -t ed25519 -f keys/probe/unenrolled -N '' -q -C unenrolled

    c_ok "generated a CA, 6 host keys, 2 upstream keys and 2 user keys"
else
    c_info "reusing keys/ (./run.sh down to regenerate)"
fi

# The certificates. Minted on every run so a stack left up overnight does not
# start failing on expiry — the failure that teaches nothing.
#
# The KEY ID is what the bastion reads as the subject, and it is what names
# the file in identities/. The two certificates differ in exactly that field.
ssh-keygen -s keys/ca -I alice@example.com -n jump,devuser,ec2-user \
    -V -5m:+8h -z 1 keys/user.pub >/dev/null 2>&1
ssh-keygen -s keys/ca -I mallory@example.com -n jump,devuser,ec2-user \
    -V -5m:+8h -z 2 keys/probe/unenrolled.pub >/dev/null 2>&1
cp keys/user keys/probe/enrolled
cp keys/user-cert.pub keys/probe/enrolled-cert.pub
chmod 600 keys/user keys/*_host_key keys/ca keys/prod keys/legacy \
    keys/probe/unenrolled keys/probe/enrolled
c_ok "minted 2 certificates (alice@example.com enrolled, mallory@example.com not)"

# ENROLMENT. One file per certificate subject, holding that person's upstream
# key, and NOTHING ELSE in the directory. The filename IS the subject: nothing
# the certificate carries is ever joined onto a path, so this directory is
# enumerated at load and looked up as a map.
#
# The public half is kept in keys/ rather than beside the private one.
# ssh-keygen writes them as a pair, and a stray .pub here would be read as an
# enrolment for a subject called "alice@example.com.pub" — rejected, correctly,
# as a public key where a private one belongs, but reported at startup as a
# problem when it is only untidiness.
if [[ ! -f "identities/alice@example.com" ]]; then
    ssh-keygen -t ed25519 -f keys/alice-upstream -N '' -q -C alice-upstream
    chmod 600 keys/alice-upstream
    cp keys/alice-upstream "identities/alice@example.com"
fi
chmod 600 "identities/alice@example.com"
c_ok "enrolled alice@example.com in identities/ (mallory@example.com is not)"

# ------------------------------------------------- authorized_keys and hosts

# host-a takes BOTH the shared key and every enrolled person's key, because
# `identities` is a LANE-WIDE overlay rather than a per-target one: an
# enrolled subject reaches every target under their own key, and only an
# unenrolled one falls back to the target's shared key. Putting just the
# shared key here would refuse alice, who is enrolled.
#
# That makes host-a the host where both halves are visible at once: alice
# arrives under her own public key, mallory under the shared one — and the
# bastion logs a fallback every time, because silent degradation is the whole
# risk an overlay carries.
cat keys/prod.pub keys/alice-upstream.pub > keys/authorized_keys.prod

# host-b takes ONE LINE PER PERSON and no shared key, which is what makes its
# target entry refuse an unenrolled subject rather than admitting them as
# somebody shared.
cp keys/alice-upstream.pub keys/authorized_keys.identities

# legacy-01 takes its own key AND every enrolled person's, for the same
# reason host-a does: the overlay is lane-wide, so `private_key` on a target
# is the credential for people who are NOT enrolled, not the credential for
# that target. This is the single most surprising property of `identities`
# and the walkthrough says so out loud.
cat keys/legacy.pub keys/alice-upstream.pub > keys/authorized_keys.legacy
chmod 644 keys/authorized_keys.*

# known_hosts, written against the NAMES the client types — which is what the
# bastion verifies under, and what ssh(1) does. The entry is the bare
# hostname, exactly as ssh(1) writes it for the default port; a "host:22"
# pattern is a different string and matches nothing.
: > keys/known_hosts
for t in host-a host-b host-c; do
    # One line per host: the pattern, then the key file's own
    # "type base64 comment". Built with sed rather than a command
    # substitution, because $(...) strips the trailing newline and would run
    # all three entries together into a single unparseable line.
    sed "s|^|${t}.prod |" "keys/${t}_host_key.pub" >> keys/known_hosts
done
chmod 644 keys/known_hosts

# legacy-01's file starts EMPTY. Its host_key_check is accept_new, so the
# bastion learns the key on first contact and writes it here — and refuses a
# CHANGED one for every session after. That is the whole difference between
# accept_new and trust-on-first-use, and part 5 of the walkthrough proves it.
# TRUNCATED ON EVERY RUN, so the walkthrough's first connection to legacy-01
# really is a first contact. Left in place it would hold a key from the last
# run, and the part of README.md that watches accept_new learn one would show
# nothing happening — the least useful way to teach a control.
mkdir -p state
: > state/known_hosts.legacy
chmod 777 state
chmod 666 state/known_hosts.legacy
c_ok "wrote known_hosts for 3 hosts; legacy-01 starts empty (accept_new)"

# ------------------------------------------------------------------- build

h "Build"

# A stamp over the two source trees, so a stale image cannot pass for a
# current one. The bastion is built from local source because ADR-0021 is not
# in a release yet.
# -L follows symlinks, and that is not optional: ../../../libhoop is usually a
# SYMLINK to a checkout beside the repo, and plain `find` reports the link
# itself rather than descending into it. Without -L the stamp covers sidecar
# alone, so a libhoop-only change leaves the image looking current and the
# stack quietly runs the old binary — the exact failure this stamp exists to
# prevent.
stamp=$(
    { find -L ../../../sidecar ../../../libhoop -name '*.go' -type f -print0 2>/dev/null \
        | sort -z | xargs -0 shasum 2>/dev/null; } | shasum | cut -c1-16
)
current=$(docker image inspect hoop-ssh-bastion:local \
    --format '{{ index .Config.Labels "dev.hoop.source-stamp" }}' 2>/dev/null || true)

if [[ "$current" != "$stamp" ]]; then
    c_info "source changed (${current:-no image} -> $stamp), rebuilding"
    SOURCE_STAMP="$stamp" "${COMPOSE[@]}" build \
        --build-arg "SOURCE_STAMP=$stamp" bastion
else
    c_ok "hoop-ssh-bastion:local is current ($stamp)"
fi
"${COMPOSE[@]}" build host-a client >/dev/null
c_ok "targets and client built"

# --------------------------------------------------------------------- up

h "Start"
# A container created against a key directory that has since been deleted and
# recreated holds a bind mount to the OLD inode, and reads an empty directory
# for the rest of its life. That failure looks exactly like "the overlay did
# not load" and is not, so freshly generated material always gets a new
# container rather than a restarted one.
# The bastion is recreated on EVERY run, not only when the keys are fresh.
# Two reasons, and both are about the stack teaching the truth:
#
#   - a container created against a key directory that has since been deleted
#     and recreated holds a bind mount to the OLD inode and reads an empty
#     directory for the rest of its life, which looks exactly like "the
#     overlay did not load" and is not;
#   - known_hosts is parsed once at load, so the file truncated above is only
#     really empty to a process that starts after it.
"${COMPOSE[@]}" up -d --force-recreate --wait bastion
"${COMPOSE[@]}" up -d --wait
c_ok "all services healthy"

# The client's learned host keys are invalidated by a regenerated CA and
# bastion key, and a stale file produces REMOTE HOST IDENTIFICATION HAS
# CHANGED on a stack where nothing is wrong. Cleared on every run because the
# keys are demo material with an eight-hour life.
"${COMPOSE[@]}" exec -T client sh -c 'rm -f /tmp/known_hosts*' 2>/dev/null || true

h "What this listener will do"
"${COMPOSE[@]}" exec -T bastion /usr/local/bin/hoop-inspect \
    --config /etc/hoop-inspect/config.yaml --validate 2>&1 \
    | sed 's/^/      /' || true

cat <<'EOF'

  Next:
    ./demo.sh                          prove it, one assertion at a time
    docker compose exec client sh      type the walkthrough yourself
    ./run.sh logs                      follow the bastion
    ./run.sh down                      stop, and delete the generated keys

  The walkthrough is README.md.
EOF
