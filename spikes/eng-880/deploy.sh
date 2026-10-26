#!/usr/bin/env bash
#
# ENG-880 spike, throwaway. Deploys stack/base.yaml in the Sandbox account.
#
#   ./deploy.sh up [Key=Value ...]   secrets, SSM, stack, images, services
#   ./deploy.sh params Key=Value ... stack update only (c1 endpoint toggles)
#   ./deploy.sh config               push stack/sidecar.yaml, restart the sidecar
#   ./deploy.sh down                 delete the stack and the SSM parameters
#
# AWS_PROFILE defaults to eng880. Secrets live in .secrets/ (gitignored).

set -euo pipefail
cd "$(dirname "$0")"
export AWS_PROFILE=${AWS_PROFILE:-eng880} AWS_REGION=${AWS_REGION:-us-east-1}
STACK=eng880-base

die() { printf 'fail %s\n' "$*" >&2; exit 1; }
out() { aws cloudformation describe-stacks --stack-name "$STACK" --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue" --output text; }

secrets() {
    mkdir -p .secrets && chmod 700 .secrets
    (
        umask 077; cd .secrets
        [[ -s caller-token ]] || openssl rand -hex 24 > caller-token
        [[ -s api-token ]] || openssl rand -hex 24 > api-token
        if [[ ! -s ca.crt ]]; then
            openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 30 \
                -subj /CN=eng880-ca -keyout ca.key -out ca.crt 2>/dev/null
            openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
                -subj /CN=sidecar.eng880.internal -keyout tls.key -out tls.csr 2>/dev/null
            openssl x509 -req -in tls.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 30 \
                -extfile <(printf 'subjectAltName=DNS:sidecar.eng880.internal\nextendedKeyUsage=serverAuth\n') \
                -out tls.crt 2>/dev/null
        fi
    )
    cp .secrets/ca.crt agent/ca.crt
}

put() { # put <name> <type> <file>
    aws ssm put-parameter --name "/eng880/$1" --type "$2" --overwrite --value "file://$3" >/dev/null
}

ssm() {
    put sidecar-yaml String stack/sidecar.yaml
    put seed-sql String stack/seed.sql
    put tls-crt String .secrets/tls.crt
    put tls-key SecureString .secrets/tls.key
    put caller-token SecureString .secrets/caller-token
    put api-token SecureString .secrets/api-token
    put license SecureString "$HOME/.hoop/secrets/hoop-dev-license.json"
}

deploy() { # deploy Key=Value ...
    aws cloudformation deploy --stack-name "$STACK" --template-file stack/base.yaml \
        --capabilities CAPABILITY_NAMED_IAM --no-fail-on-empty-changeset \
        --tags Project=eng-880 --parameter-overrides "$@"
}

images() {
    local repo; repo=$(out RepoUri)
    aws ecr get-login-password | docker login --username AWS --password-stdin "${repo%%/*}" >/dev/null
    docker buildx build --platform linux/arm64 --provenance=false -t "$repo:probe" -f stack/Dockerfile.probe stack --push
    docker buildx build --platform linux/arm64 --provenance=false -t "$repo:agent" agent --push
}

case "${1:-}" in
    up)
        shift
        secrets && ssm
        deploy ServicesUp=0 "$@"
        images
        deploy ServicesUp=1 "$@"
        aws ecs wait services-stable --cluster eng880 --services sidecar probe db
        aws cloudformation describe-stacks --stack-name "$STACK" --query 'Stacks[0].Outputs' --output table
        ;;
    params)
        shift; deploy "$@" ;;
    config)
        put sidecar-yaml String stack/sidecar.yaml
        aws ecs update-service --cluster eng880 --service sidecar --force-new-deployment >/dev/null
        aws ecs wait services-stable --cluster eng880 --services sidecar ;;
    down)
        aws cloudformation delete-stack --stack-name "$STACK"
        aws cloudformation wait stack-delete-complete --stack-name "$STACK"
        aws ssm delete-parameters --names /eng880/{sidecar-yaml,seed-sql,tls-crt,tls-key,caller-token,api-token,license} >/dev/null ;;
    *) die "usage: $0 up|params|config|down (see the header)" ;;
esac
