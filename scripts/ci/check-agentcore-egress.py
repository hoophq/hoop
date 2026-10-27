#!/usr/bin/env python3
"""Fail when an AgentCore template opens a way out of the agent subnets
that skips the sidecar (ENG-882 and ENG-885, from the ENG-880 spike).

The agent network is what the template's harness or Runtime agent names in
its NetworkModeConfig, and what its AgentSubnetIds and AgentSecurityGroupId
outputs hand to a customer's own Runtime agent. The two must be the same.
For that network:

1. Routes. Each agent subnet has an explicit route table. No route, route
   propagation or gateway endpoint other than S3 touches that table.
2. Endpoints and egress. Interface endpoints serve only ECR, Bedrock
   Runtime, CloudWatch Logs and AgentCore, and admit only this account's
   principals: the default policy takes any account's keys. The agent group
   lists its egress inline, or AWS adds allow-all; each rule is TCP to the
   endpoints' group on 443, to the sidecar's group on one port, or to the
   S3PrefixListId parameter on 443. The sidecar's group is one an ECS
   service carries and the agent reaches; a service behind the sidecar,
   such as a database, has its own. The endpoints' group and the sidecar's
   group are on no other resource, the sidecar's group runs only the hoop
   sidecar image, and only the agent group may reach it.
3. S3. The gateway endpoint allows only s3:GetObject on
   prod-<region>-starport-layer-bucket, where ECR keeps image layers.
4. DNS. The VPC has a Resolver DNS Firewall association that blocks '*.'
   for every query type after its allow rules; no allow list holds a
   wildcard wider than one domain, and no Resolver rule forwards the VPC's
   names elsewhere.

A resource that holds one of these up must not carry a Condition: no
parameter may turn a guarantee off.

Usage: scripts/ci/check-agentcore-egress.py <template>...
Needs: python3 3.10 or later, cfn-lint (its decoder keeps !Ref and !GetAtt).
Tests: scripts/ci/check-agentcore-egress_test.py
"""

import re
import sys

from cfnlint.decode import decode

AGENT_TYPES = {
    "AWS::BedrockAgentCore::Harness": ("Environment", "AgentCoreRuntimeEnvironment", "NetworkConfiguration"),
    "AWS::BedrockAgentCore::Runtime": ("NetworkConfiguration",),
}
# ${AWS::Region}, or a Region name such as us-east-1 or us-gov-west-1: a
# looser pattern lets prod-evil-starport-layer-bucket, anyone's to create, pass.
REGION = r"(\$\{AWS::Region\}|[a-z]{2}(-gov|-iso[a-z]?)?-[a-z]+-[0-9])"
INTERFACE_SERVICE = re.compile(rf"^com\.amazonaws\.{REGION}\.(ecr\.dkr|ecr\.api|bedrock-runtime|logs|bedrock-agentcore)$")
S3_SERVICE = re.compile(rf"^com\.amazonaws\.{REGION}\.s3$")
LAYER_BUCKET = re.compile(
    rf"^arn:(aws[a-z-]*|\$\{{AWS::Partition\}}):s3:::prod-{REGION}-starport-layer-bucket/\*$")
# A rule to these groups names them; the groups themselves are attached here.
GROUP_RULE_TYPES = {"AWS::EC2::SecurityGroup", "AWS::EC2::SecurityGroupIngress", "AWS::EC2::SecurityGroupEgress"}
# '*.', or '*.' and one label: every name, or every name under a TLD.
TOO_WIDE = re.compile(r"^\*(\.[^.]*)?\.?$")


def name_of(value):
    """The logical id behind a Ref or a GetAtt, else None."""
    if isinstance(value, dict) and len(value) == 1:
        if "Ref" in value:
            return value["Ref"]
        if "Fn::GetAtt" in value:
            att = value["Fn::GetAtt"]
            return (att.split(".", 1) if isinstance(att, str) else att)[0]
    return None


def names_of(value):
    """The logical ids in a list, a Ref, or a ',' Fn::Join of Refs."""
    if isinstance(value, dict) and "Fn::Join" in value:
        value = value["Fn::Join"][1]
    items = value if isinstance(value, list) else [value]
    return {name_of(v) for v in items}


def text_of(value):
    """A literal string, or the string of an Fn::Sub without variables."""
    if isinstance(value, dict) and isinstance(value.get("Fn::Sub"), str):
        return value["Fn::Sub"]
    return value if isinstance(value, str) else None


def as_list(value):
    return value if isinstance(value, list) else [value]


def dig(props, *path):
    for key in path:
        if not isinstance(props, dict):
            return None
        props = props.get(key)
    return props


def strings_in(value):
    """Every literal string anywhere in value."""
    if isinstance(value, str):
        return [value]
    if isinstance(value, dict):
        return [x for v in value.values() for x in strings_in(v)]
    if isinstance(value, list):
        return [x for v in value for x in strings_in(v)]
    return []


def has_key(value, key):
    if isinstance(value, dict):
        return key in value or any(has_key(v, key) for v in value.values())
    if isinstance(value, list):
        return any(has_key(v, key) for v in value)
    return False


def mentions(value, name):
    """Whether a Ref, a GetAtt or an Fn::Sub variable anywhere in value names name."""
    if isinstance(value, dict):
        sub = value.get("Fn::Sub")
        text = sub[0] if isinstance(sub, list) and sub else sub
        # ${name} or ${name.Attribute}; ${!name} is literal text.
        if isinstance(text, str) and re.search(rf"\$\{{{re.escape(name)}(\.[^}}]*)?\}}", text):
            return True
        return name_of(value) == name or any(mentions(v, name) for v in value.values())
    if isinstance(value, list):
        return any(mentions(v, name) for v in value)
    return False


def check(template):
    res = template.get("Resources", {})
    params = template.get("Parameters", {})
    errors = []

    def of_type(kind):
        return {k: r for k, r in res.items() if r.get("Type") == kind}

    def props(name):
        return res.get(name, {}).get("Properties", {})

    def unconditional(name, why):
        if "Condition" in res.get(name, {}):
            errors.append(f"{name} carries Condition {res[name]['Condition']}: {why} must not depend on a parameter")

    def rules_of(group, inline_key, kind):
        inline = props(group).get(inline_key)
        rules = [(f"{group} inline {i}", r) for i, r in enumerate(as_list(inline or []))]
        rules += [(k, r["Properties"]) for k, r in of_type(kind).items() if name_of(r["Properties"].get("GroupId")) == group]
        return inline, rules

    # The agent network, from the agents and from the outputs.
    outputs = template.get("Outputs", {})
    for out in ("AgentSubnetIds", "AgentSecurityGroupId"):
        if out not in outputs:
            errors.append(f"no {out} output: a customer's own Runtime agent needs it")
    subnets = names_of(dig(outputs, "AgentSubnetIds", "Value"))
    groups = names_of(dig(outputs, "AgentSecurityGroupId", "Value"))
    for name, kind in ((k, r["Type"]) for k, r in res.items() if r.get("Type") in AGENT_TYPES):
        net = dig(props(name), *AGENT_TYPES[kind])
        if dig(net, "NetworkMode") != "VPC":
            errors.append(f"{name} runs with NetworkMode {dig(net, 'NetworkMode')}, not VPC: it would skip the VPC")
            continue
        for field, want in (("Subnets", subnets), ("SecurityGroups", groups)):
            got = names_of(dig(net, "NetworkModeConfig", field))
            if got != want:
                errors.append(f"{name} {field} {sorted(map(str, got))} differ from the outputs' {sorted(map(str, want))}")
    # A literal id or an expression names nothing this check can follow.
    for what, names in (("subnet", subnets), ("security group", groups)):
        if None in names:
            errors.append(f"an agent {what} is named by a literal id or an expression: "
                          "only a resource in this template can be checked")
    if errors:
        return errors

    # An Fn::If (or any expression) in these ids can pick an agent subnet,
    # an agent group or another route table without the check seeing it.
    for kind, fields in (("AWS::EC2::SubnetRouteTableAssociation", ("SubnetId", "RouteTableId")),
                         ("AWS::EC2::SecurityGroupEgress", ("GroupId",)),
                         ("AWS::EC2::SecurityGroupIngress", ("GroupId",))):
        for name, r in of_type(kind).items():
            for field in fields:
                if name_of(r["Properties"].get(field)) is None:
                    errors.append(f"{name} names its {field} by an expression or a literal id: "
                                  "only a resource in this template can be checked")
    if errors:
        return errors

    # 1. Routes.
    tables = set()
    vpcs = {name_of(props(s).get("VpcId")) for s in subnets}
    for subnet in sorted(subnets):
        assoc = [k for k, r in of_type("AWS::EC2::SubnetRouteTableAssociation").items()
                 if name_of(r["Properties"].get("SubnetId")) == subnet]
        if not assoc:
            errors.append(f"agent subnet {subnet} has no route table association: it would use the main route table")
        for a in assoc:
            unconditional(a, "an agent subnet's route table")
            tables.add(name_of(props(a).get("RouteTableId")))
    for name, route in of_type("AWS::EC2::Route").items():
        p = route["Properties"]
        table = name_of(p.get("RouteTableId"))
        dest = p.get("DestinationCidrBlock") or p.get("DestinationIpv6CidrBlock") or p.get("DestinationPrefixListId")
        if table is None:
            errors.append(f"route {name} names its route table by an expression: it may be the agent route table")
        elif table in tables:
            errors.append(f"route {name} sends {dest} from an agent subnet: the agent route table must hold no route")
    for kind in ("AWS::EC2::VPNGatewayRoutePropagation", "AWS::EC2::RouteServerPropagation"):
        for name, r in of_type(kind).items():
            p = r["Properties"]
            if names_of(p.get("RouteTableIds", p.get("RouteTableId", []))) & (tables | {None}):
                errors.append(f"{name} propagates routes into an agent route table")

    # 2. Endpoints and egress.
    endpoint_groups, s3_endpoints = set(), []
    for name, ep in of_type("AWS::EC2::VPCEndpoint").items():
        p = ep["Properties"]
        # CloudFormation's default endpoint type is Gateway.
        kind, service = p.get("VpcEndpointType", "Gateway"), text_of(p.get("ServiceName"))
        if kind == "Interface":
            if service and INTERFACE_SERVICE.match(service):
                endpoint_groups |= names_of(p.get("SecurityGroupIds", []))
                allows = [st for st in as_list(dig(p, "PolicyDocument", "Statement") or [])
                          if st.get("Effect") != "Deny"]
                if not allows or any(dig(st, "Condition", "StringEquals", "aws:PrincipalAccount")
                                     != {"Ref": "AWS::AccountId"} for st in allows):
                    errors.append(f"interface endpoint {name} admits any account's keys: each Allow statement "
                                  "needs aws:PrincipalAccount = AWS::AccountId")
                unconditional(name, "a VPC endpoint")
            else:
                errors.append(f"interface endpoint {name} serves {p.get('ServiceName')}: only ECR, Bedrock Runtime, "
                              "CloudWatch Logs and AgentCore")
        elif kind == "Gateway":
            if not names_of(p.get("RouteTableIds", [])) & (tables | {None}):
                continue
            if service and S3_SERVICE.match(service):
                s3_endpoints.append(name)
            else:
                errors.append(f"gateway endpoint {name} ({p.get('ServiceName')}) routes from an agent subnet: only S3 may")
        else:
            errors.append(f"endpoint {name} has type {kind}: only Interface and Gateway endpoints are known")
    endpoint_groups.discard(None)
    services = of_type("AWS::ECS::Service")

    def service_groups(svc):
        return names_of(dig(svc["Properties"], "NetworkConfiguration", "AwsvpcConfiguration", "SecurityGroups") or [])

    # An expression can put the sidecar's group on any service.
    for name, svc in services.items():
        if None in service_groups(svc):
            errors.append(f"{name} names its security groups by an expression or a literal id: "
                          "only a resource in this template can be checked")
    if errors:
        return errors

    agent_rules = []
    for group in sorted(groups):
        if res.get(group, {}).get("Type") != "AWS::EC2::SecurityGroup":
            errors.append(f"agent security group {group} is not an AWS::EC2::SecurityGroup in this template")
            continue
        unconditional(group, "the agent security group")
        inline, rules = rules_of(group, "SecurityGroupEgress", "AWS::EC2::SecurityGroupEgress")
        if not inline:
            errors.append(f"{group} lists no egress inline: AWS then allows all egress to 0.0.0.0/0")
        agent_rules += rules
    # Only a group the agent reaches counts, so a database's group is not one.
    reached = {name_of(r.get("DestinationSecurityGroupId")) for _, r in agent_rules}
    sidecar_groups = (set().union(*map(service_groups, services.values())) & reached) - endpoint_groups

    for where, rule in agent_rules:
        target = name_of(rule.get("DestinationSecurityGroupId")) or rule.get("DestinationSecurityGroupId")
        ports = (rule.get("FromPort"), rule.get("ToPort"))
        prefix = rule.get("DestinationPrefixListId")
        if str(rule.get("IpProtocol")) not in ("tcp", "6"):
            errors.append(f"{where}: protocol {rule.get('IpProtocol')}, not tcp")
        elif rule.get("CidrIp") or rule.get("CidrIpv6"):
            errors.append(f"{where}: egress to {rule.get('CidrIp') or rule.get('CidrIpv6')}; "
                          "only the endpoints, the sidecar and the S3 prefix list")
        elif prefix is not None:
            if name_of(prefix) != "S3PrefixListId" or "S3PrefixListId" not in params:
                errors.append(f"{where}: egress to prefix list {prefix}, not the S3PrefixListId parameter")
            elif ports != (443, 443):
                errors.append(f"{where}: ports {ports} to S3, not 443")
        elif target in endpoint_groups:
            if ports != (443, 443):
                errors.append(f"{where}: ports {ports} to the endpoints, not 443")
        elif target in sidecar_groups:
            # A TCP rule with no ports opens every port.
            if not isinstance(ports[0], int) or ports[0] != ports[1]:
                errors.append(f"{where}: ports {ports} to the sidecar, not one port")
        else:
            errors.append(f"{where}: egress to {target}, which is neither the endpoints, the sidecar "
                          "nor the S3 prefix list")

    # A group the agent reaches is a destination for whatever carries it.
    for group, owner in [(g, "AWS::EC2::VPCEndpoint") for g in endpoint_groups] + \
                        [(g, "AWS::ECS::Service") for g in sidecar_groups]:
        for name, r in res.items():
            kind = r.get("Type")
            if kind not in GROUP_RULE_TYPES | {owner} and mentions(r.get("Properties", {}), group):
                errors.append(f"{name} ({kind}) carries {group}, which the agent reaches")
    for name, svc in services.items():
        if not service_groups(svc) & sidecar_groups:
            continue
        task_name = name_of(svc["Properties"].get("TaskDefinition"))
        unconditional(name, "the sidecar service")
        unconditional(task_name, "the sidecar task")
        # An expression can pick any task: the image check below sees none.
        if res.get(task_name, {}).get("Type") != "AWS::ECS::TaskDefinition" or \
                not props(task_name).get("ContainerDefinitions"):
            errors.append(f"{name} runs {svc['Properties'].get('TaskDefinition')} in a group the agent reaches: "
                          "only a task definition in this template can be checked")
        for c in props(task_name).get("ContainerDefinitions", []):
            image = c.get("Image")
            param = params.get(name_of(image) or "", {})
            default, pattern = str(param.get("Default", "")), param.get("AllowedPattern", "")
            if not default.startswith("hoophq/hoopsidecar:"):
                errors.append(f"{name} runs {image} in a group the agent reaches: only the hoop sidecar image")
            # A deployment can override the default: the pattern holds the image.
            elif "hoophq/hoopsidecar:" not in pattern or not re.fullmatch(pattern, default):
                errors.append(f"{name}'s image parameter needs an AllowedPattern for hoophq/hoopsidecar "
                              "that its default matches")
    for group in sorted(sidecar_groups):
        _, rules = rules_of(group, "SecurityGroupIngress", "AWS::EC2::SecurityGroupIngress")
        for where, rule in rules:
            if name_of(rule.get("SourceSecurityGroupId")) not in groups:
                src = rule.get("CidrIp") or rule.get("CidrIpv6") or rule.get("SourcePrefixListId") or \
                    rule.get("SourceSecurityGroupId")
                errors.append(f"{where}: ingress to the sidecar from {src}; only the agent group may reach it")

    # 3. S3.
    for name in s3_endpoints:
        unconditional(name, "the S3 gateway endpoint")
        policy = props(name).get("PolicyDocument")
        if not policy:
            errors.append(f"{name} has no PolicyDocument: the default allows every bucket")
            continue
        for i, st in enumerate(as_list(policy.get("Statement", []))):
            if st.get("Effect") == "Deny":
                continue
            resources = [text_of(r) for r in as_list(st.get("Resource"))]
            if {"NotAction", "NotResource", "NotPrincipal"} & st.keys():
                errors.append(f"{name} statement {i} uses a Not* element")
            elif as_list(st.get("Action")) != ["s3:GetObject"]:
                errors.append(f"{name} statement {i} allows {st.get('Action')}, not only s3:GetObject")
            elif not all(r and LAYER_BUCKET.match(r) for r in resources):
                errors.append(f"{name} statement {i} allows {resources}, not only the ECR layer bucket")

    # 4. DNS.
    for name, r in of_type("AWS::Route53Resolver::ResolverRuleAssociation").items():
        if name_of(r["Properties"].get("VPCId")) in vpcs | {None}:
            errors.append(f"{name} attaches a Resolver rule to the agent VPC: it can forward names to another server")
    associations = of_type("AWS::Route53Resolver::FirewallRuleGroupAssociation")
    for vpc in sorted(map(str, vpcs)):
        found = [k for k, r in associations.items() if name_of(r["Properties"].get("VpcId")) == vpc]
        if not found:
            errors.append(f"VPC {vpc} has no Route 53 Resolver DNS Firewall association: it answers any public name")
        for assoc in found:
            unconditional(assoc, "the DNS Firewall association")
            group = name_of(props(assoc).get("FirewallRuleGroupId"))
            unconditional(group, "the DNS Firewall rule group")
            allow, block = [], []
            for rule in props(group).get("FirewallRules", []):
                domains = name_of(rule.get("FirewallDomainListId"))
                unconditional(domains, "a DNS Firewall domain list")
                entries = props(domains).get("Domains", [])
                names = [d for d in entries if isinstance(d, str)]
                if rule.get("Action") == "ALLOW":
                    allow.append(rule.get("Priority"))
                    if res.get(domains, {}).get("Type") != "AWS::Route53Resolver::FirewallDomainList":
                        errors.append(f"{group} allows domain list {rule.get('FirewallDomainListId')}, "
                                      "which is not in this template")
                    for d in names:
                        if TOO_WIDE.match(d):
                            errors.append(f"{domains} allows '{d}': wider than one domain")
                    # An expression may resolve to '*.': only a wildcard-free,
                    # unconditional one passes, such as the upstream host.
                    for d in (e for e in entries if not isinstance(e, str)):
                        if has_key(d, "Fn::If") or any("*" in x for x in strings_in(d)):
                            errors.append(f"{domains} allows {d}: a condition or a wildcard in an expression")
                # A rule with a Qtype blocks that query type only.
                elif rule.get("Action") == "BLOCK" and "Qtype" not in rule and {"*.", "*"} & set(names):
                    block.append(rule.get("Priority"))
            if not block or (allow and max(block) <= max(allow)):
                errors.append(f"DNS Firewall rule group {group} does not block '*.' after its allow rules")
    return errors


def main(paths):
    if not paths:
        print("usage: check-agentcore-egress.py <template>...", file=sys.stderr)
        return 2
    failed = 0
    for path in paths:
        template, matches = decode(path)
        if template is None:
            for m in matches:
                print(f"::error file={path}::cannot read the template: {m}")
            failed += 1
            continue
        errors = check(template)
        for e in errors:
            print(f"::error file={path}::{e}")
        if errors:
            failed += 1
        else:
            print(f"ok {path}: the agent network leaves only through the endpoints and the sidecar")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
