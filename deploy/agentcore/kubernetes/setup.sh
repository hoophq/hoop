#!/usr/bin/env bash
#
# Creates the template.yaml stack in front of one EKS cluster, builds the
# harness's kubectl image, routes the cluster's VPC back to the sidecar, and
# asks the harness a question. README.md has the steps.
#
# Usage:
#   ./setup.sh create              deploy the stack and the image, add the return routes
#   ./setup.sh session <question>  ask the harness; print its commands and answer
#   ./setup.sh delete              delete the return routes, then the stack
#
# create reads CLUSTER (the EKS cluster's name), KUBE_TOKEN_SECRET_ARN, and
# SIDECAR_TOKEN_FILE or LICENSE_FILE. Optional: AGENT (harness or none,
# default harness), KUBE_USER and KUBE_NAMESPACE (default rbac.yaml's),
# MODEL_ID, AGENT_AZ_IDS (two AgentCore zone ids, required outside
# us-east-1), HARNESS_NAME (default: STACK with _ for -), NEW_LANE_CERT=1 to
# replace the lane's certificate. All commands read STACK (default
# hoop-agentcore-kubernetes) and the AWS CLI's profile and Region.
# Needs the AWS CLI v2, jq, python3, openssl, and curl 7.75 or later; create
# with a harness also needs docker with buildx.

set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
stack=${STACK:-hoop-agentcore-kubernetes}
# The stack's VPC (template.yaml). The peering needs a cluster VPC apart from it.
stack_cidr=10.90.0.0/16

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in aws jq python3 openssl curl; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
region=${AWS_REGION:-${AWS_DEFAULT_REGION:-$(aws configure get region || true)}}
[[ -n "$region" ]] || die "no AWS Region: set AWS_REGION"
export AWS_REGION=$region

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
umask 077

output() {
    aws cloudformation describe-stacks --stack-name "$stack" \
        --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue" --output text
}
parameter() {
    aws cloudformation describe-stacks --stack-name "$stack" \
        --query "Stacks[0].Parameters[?ParameterKey=='$1'].ParameterValue" --output text
}

# The cluster's endpoint must be private only: with public access on too, its
# name resolves to public addresses outside the cluster's VPC, and the
# sidecar would leave through the internet.
read_cluster() {
    aws eks describe-cluster --name "$CLUSTER" --query cluster --output json > "$tmp/cluster.json"
    jq -e '.resourcesVpcConfig.endpointPrivateAccess' "$tmp/cluster.json" >/dev/null \
        || die "cluster $CLUSTER has no private endpoint: turn on endpointPrivateAccess"
    jq -e '.resourcesVpcConfig.endpointPublicAccess | not' "$tmp/cluster.json" >/dev/null \
        || die "cluster $CLUSTER's endpoint is public too: turn off endpointPublicAccess, or the sidecar reaches it through the internet"
    local vpc subnets
    vpc=$(jq -r .resourcesVpcConfig.vpcId "$tmp/cluster.json")
    read -ra subnets < <(jq -r '.resourcesVpcConfig.subnetIds | join(" ")' "$tmp/cluster.json")
    aws ec2 describe-vpcs --vpc-ids "$vpc" --query 'Vpcs[0]' --output json > "$tmp/vpc.json"
    aws ec2 describe-subnets --subnet-ids "${subnets[@]}" --query 'Subnets[].CidrBlock' --output json \
        > "$tmp/subnets.json"
    # The stack routes to the VPC's primary CIDR, so the cluster's subnets must be in it.
    python3 - "$stack_cidr" "$tmp/vpc.json" "$tmp/subnets.json" <<'EOF' || die "cluster $CLUSTER's VPC cannot peer with this stack"
import ipaddress, json, sys
stack = ipaddress.ip_network(sys.argv[1])
vpc = json.load(open(sys.argv[2]))
primary = ipaddress.ip_network(vpc["CidrBlock"])
for a in vpc.get("CidrBlockAssociationSet", []):
    if ipaddress.ip_network(a["CidrBlock"]).overlaps(stack):
        sys.exit(f"{vpc['VpcId']} holds {a['CidrBlock']}, which overlaps the stack's VPC {stack}")
for cidr in json.load(open(sys.argv[3])):
    if not ipaddress.ip_network(cidr).subnet_of(primary):
        sys.exit(f"cluster subnet {cidr} is outside {vpc['VpcId']}'s primary CIDR {primary}")
EOF
}

# $1: AgentKind, $2: HarnessImage. A file, not argv: the process list would
# show the sidecar's credential. An update keeps the old value of a
# parameter it does not name: the lane's certificate when no new one was
# made, and the unused credential goes empty, not absent.
deploy() {
    : "${prefix_list:?}" "${harness_name:?}" "${cred_key:?}" "${cred_file:?}"
    [[ -s "$tmp/lane.json" && -s "$tmp/cluster.json" && -s "$tmp/vpc.json" ]] || die "deploy runs after read_cluster"
    jq -n --arg agent "$1" --arg image "$2" --slurpfile cluster "$tmp/cluster.json" \
        --arg cidr "$(jq -r .CidrBlock "$tmp/vpc.json")" --slurpfile lane "$tmp/lane.json" \
        --arg token "${KUBE_TOKEN_SECRET_ARN:-}" --arg pl "$prefix_list" --arg harness "$harness_name" \
        --arg key "$cred_key" --rawfile cred "$cred_file" \
        --arg user "${KUBE_USER:-}" --arg ns "${KUBE_NAMESPACE:-}" --arg model "${MODEL_ID:-}" \
        --arg azs "${AGENT_AZ_IDS:-}" \
        '$cluster[0] as $c
         | {AgentKind: $agent, HarnessImage: $image, KubeTokenSecretArn: $token,
            ClusterEndpoint: $c.endpoint, ClusterCaData: $c.certificateAuthority.data,
            ClusterVpcId: $c.resourcesVpcConfig.vpcId, ClusterVpcCidr: $cidr,
            ClusterSecurityGroupId: $c.resourcesVpcConfig.clusterSecurityGroupId,
            S3PrefixListId: $pl, HarnessName: $harness, HoopSidecarToken: "", HoopLicense: ""}
         + {($key): ($cred | rtrimstr("\n"))} + $lane[0]
         + ({KubeUser: $user, KubeNamespace: $ns, ModelId: $model, AgentAzIds: $azs}
            | with_entries(select(.value != "")))
         | to_entries | map({ParameterKey: .key, ParameterValue: .value})' > "$tmp/parameters.json"
    aws cloudformation deploy --stack-name "$stack" --template-file "$here/template.yaml" \
        --capabilities CAPABILITY_IAM --no-fail-on-empty-changeset \
        --parameter-overrides "file://$tmp/parameters.json"
}

# Prints the image by digest. kubectl stays within one minor release of the
# API server: the latest patch of the cluster's own minor release.
build_image() {
    local repo=$1 minor version tag digest
    minor=$(jq -r .version "$tmp/cluster.json")
    version=$(curl -fsS "https://dl.k8s.io/release/stable-$minor.txt") \
        || die "no kubectl release for Kubernetes $minor at dl.k8s.io"
    [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "dl.k8s.io answered '$version' for Kubernetes $minor"
    tag=kubectl-$version
    aws ecr get-login-password | docker login --username AWS --password-stdin "${repo%%/*}" >/dev/null
    docker buildx build --platform linux/arm64 --provenance=false --build-arg "KUBECTL_VERSION=$version" \
        -t "$repo:$tag" --push "$here/image" >&2
    digest=$(aws ecr describe-images --repository-name "${repo#*/}" --image-ids "imageTag=$tag" \
        --query 'imageDetails[0].imageDigest' --output text)
    [[ "$digest" == sha256:* ]] || die "ECR has no digest for $repo:$tag"
    printf '%s@%s\n' "$repo" "$digest"
}

# The route tables of the cluster's subnets; a subnet without its own uses
# the VPC's main table.
cluster_route_tables() {
    jq -r --slurpfile c "$tmp/cluster.json" '$c[0].resourcesVpcConfig.subnetIds as $ids
        | [.RouteTables[] | select(any(.Associations[]?; .SubnetId as $s | $ids | index($s)))] as $own
        | ($own | map(.RouteTableId))
          + (if ($ids - [$own[].Associations[]?.SubnetId]) == [] then []
             else [.RouteTables[] | select(any(.Associations[]?; .Main)) | .RouteTableId] end)
        | unique[]' "$tmp/tables.json"
}

# The cluster answers the sidecar through the peering. Only the sidecar's
# subnet is routed back, never the agent subnets.
add_return_routes() {
    local peering cidr t target
    peering=$(output PeeringConnectionId)
    cidr=$(output SidecarSubnetCidr)
    aws ec2 describe-route-tables --filters "Name=vpc-id,Values=$(jq -r .VpcId "$tmp/vpc.json")" \
        --output json > "$tmp/tables.json"
    for t in $(cluster_route_tables); do
        target=$(jq -r --arg t "$t" --arg c "$cidr" '.RouteTables[] | select(.RouteTableId == $t) | .Routes[]
            | select(.DestinationCidrBlock == $c)
            | if .State == "blackhole" then "blackhole"
              else .VpcPeeringConnectionId // .GatewayId // .NatGatewayId // .TransitGatewayId
                   // .NetworkInterfaceId // "another target" end' "$tmp/tables.json")
        if [[ -z "$target" ]]; then
            aws ec2 create-route --route-table-id "$t" --destination-cidr-block "$cidr" \
                --vpc-peering-connection-id "$peering" >/dev/null
            echo "route table $t: $cidr through $peering"
        # A stack deleted without setup.sh leaves its route behind, pointing nowhere.
        elif [[ "$target" == blackhole ]]; then
            aws ec2 replace-route --route-table-id "$t" --destination-cidr-block "$cidr" \
                --vpc-peering-connection-id "$peering" >/dev/null
            echo "route table $t: $cidr through $peering, in place of a blackhole route"
        elif [[ "$target" != "$peering" ]]; then
            die "route table $t already sends $cidr to $target: remove that route, then run create again"
        fi
    done
}

create() {
    : "${CLUSTER:?set CLUSTER to the EKS cluster name}"
    agent=${AGENT:-harness}
    [[ "$agent" == harness || "$agent" == none ]] || die "AGENT is harness or none, not $agent"
    if [[ "$agent" == harness ]]; then
        : "${KUBE_TOKEN_SECRET_ARN:?set KUBE_TOKEN_SECRET_ARN to the secret that holds the agent token (README.md)}"
        command -v docker >/dev/null || die "missing required tool: docker"
    fi
    if [[ -n "${SIDECAR_TOKEN_FILE:-}" ]]; then cred_key=HoopSidecarToken cred_file=$SIDECAR_TOKEN_FILE
    elif [[ -n "${LICENSE_FILE:-}" ]]; then cred_key=HoopLicense cred_file=$LICENSE_FILE
    else die "set SIDECAR_TOKEN_FILE (a use.hoop.dev sidecar token) or LICENSE_FILE"; fi
    [[ -s "$cred_file" ]] || die "$cred_file is missing or empty"
    [[ "$region" == us-east-1 || -n "${AGENT_AZ_IDS:-}" ]] \
        || die "set AGENT_AZ_IDS to two AgentCore zone ids of $region, such as euc1-az1,euc1-az2"

    local status repo image
    # A first create that failed leaves the stack, empty, in this state; a
    # deploy cannot update it.
    status=$(aws cloudformation describe-stacks --stack-name "$stack" --query 'Stacks[0].StackStatus' \
        --output text 2>/dev/null || true)
    [[ "$status" != ROLLBACK_COMPLETE ]] || die "stack $stack failed to create: run ./setup.sh delete, then create again"
    prefix_list=$(aws ec2 describe-managed-prefix-lists \
        --filters "Name=prefix-list-name,Values=com.amazonaws.$region.s3" \
        --query 'PrefixLists[0].PrefixListId' --output text)
    [[ "$prefix_list" == pl-* ]] || die "no S3 prefix list in $region"
    # A harness name is unique per Region, so each stack names its own.
    harness_name=${HARNESS_NAME:-$(printf %s "$stack" | tr -c 'A-Za-z0-9' _ | cut -c1-40)}
    read_cluster
    # Another VPC replaces the peering, and delete then cannot find the
    # routes left in the old one.
    local old_vpc
    old_vpc=$(parameter ClusterVpcId 2>/dev/null || true)
    [[ -z "$old_vpc" || "$old_vpc" == None || "$old_vpc" == "$(jq -r .VpcId "$tmp/vpc.json")" ]] \
        || die "stack $stack fronts a cluster in $old_vpc: run ./setup.sh delete first, or set another STACK"

    # The lane's certificate, for sidecar.hoop.internal. Made once; an update
    # keeps it unless NEW_LANE_CERT is set.
    echo '{}' > "$tmp/lane.json"
    if [[ -n "${NEW_LANE_CERT:-}" || -z "$(parameter LaneCertificate 2>/dev/null || true)" ]]; then
        openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 \
            -keyout "$tmp/lane.key" -out "$tmp/lane.crt" -subj /CN=sidecar.hoop.internal \
            -addext subjectAltName=DNS:sidecar.hoop.internal 2>"$tmp/openssl.err" \
            || die "openssl could not make the lane certificate: $(cat "$tmp/openssl.err")"
        # kubectl ignores the common name: without the SAN, every TLS handshake fails.
        openssl x509 -noout -ext subjectAltName -in "$tmp/lane.crt" 2>/dev/null | grep -q 'DNS:sidecar.hoop.internal' \
            || die "$(openssl version) made the lane certificate without its subjectAltName: use OpenSSL 3"
        jq -n --arg crt "$(openssl base64 -A -in "$tmp/lane.crt")" --arg key "$(openssl base64 -A -in "$tmp/lane.key")" \
            '{LaneCertificate: $crt, LaneKey: $key}' > "$tmp/lane.json"
        rm -f "$tmp/lane.key"
    fi

    # The harness needs its image, and the image needs the stack's repository:
    # a first create deploys without the harness, then with it.
    repo=$(output AgentRepositoryUri 2>/dev/null || true)
    if [[ "$agent" == none || -z "$repo" || "$repo" == None ]]; then
        echo "deploying stack $stack in $region; a first create takes about 8 minutes..."
        deploy none ''
        echo '{}' > "$tmp/lane.json"
        repo=$(output AgentRepositoryUri)
    fi
    if [[ "$agent" == harness ]]; then
        echo "building the harness image with kubectl for Kubernetes $(jq -r .version "$tmp/cluster.json")..."
        image=$(build_image "$repo")
        echo "deploying the harness with $image..."
        deploy harness "$image"
    fi
    add_return_routes
    aws cloudformation describe-stacks --stack-name "$stack" --query 'Stacks[0].Outputs' --output table
}

delete() {
    local peering cidr vpc t
    peering=$(output PeeringConnectionId 2>/dev/null || true)
    if [[ -n "$peering" && "$peering" != None ]]; then
        cidr=$(output SidecarSubnetCidr)
        vpc=$(parameter ClusterVpcId)
        aws ec2 describe-route-tables --filters "Name=vpc-id,Values=$vpc" \
            "Name=route.vpc-peering-connection-id,Values=$peering" --output json > "$tmp/tables.json"
        # Only the route create added: an operator's own routes stay theirs.
        for t in $(jq -r --arg p "$peering" --arg c "$cidr" '.RouteTables[]
            | select(any(.Routes[]; .VpcPeeringConnectionId == $p and .DestinationCidrBlock == $c))
            | .RouteTableId' "$tmp/tables.json"); do
            aws ec2 delete-route --route-table-id "$t" --destination-cidr-block "$cidr"
            echo "route table $t: removed $cidr through $peering"
        done
    fi
    aws cloudformation delete-stack --stack-name "$stack"
    echo "deleting stack $stack..."
    aws cloudformation wait stack-delete-complete --stack-name "$stack" \
        || die "stack $stack did not delete. AgentCore can hold network interfaces in the agent subnets for hours; run ./setup.sh delete again later"
    echo "deleted stack $stack"
}

case "${1:-}" in
    create) create ;;
    # The http template's session works for any stack with a HarnessArn output.
    session) STACK=$stack exec "$here/../http/setup.sh" session "${@:2}" ;;
    delete) delete ;;
    *) die "usage: ./setup.sh create | session <question> | delete" ;;
esac
