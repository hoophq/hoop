#!/usr/bin/env python3
"""Tests for check-agentcore-egress.py. A check that cannot fail passes
forever, so each case breaks one guarantee of the real template and expects
the check to name it.

Usage: scripts/ci/check-agentcore-egress_test.py   (from the repo root)
"""

import copy
import json
import os
import re
import subprocess
import sys
import tempfile

from cfnlint.decode import decode
from cfnlint.schema import PROVIDER_SCHEMA_MANAGER

HERE = os.path.dirname(os.path.abspath(__file__))
CHECK = os.path.join(HERE, "check-agentcore-egress.py")
TEMPLATE = os.path.join(HERE, "..", "..", "deploy", "agentcore", "http", "template.yaml")


def res(t, name):
    return t["Resources"][name]


def add(t, name, kind, props, **extra):
    t["Resources"][name] = {"Type": kind, "Properties": props, **extra}


def harness_net(t):
    return res(t, "Harness")["Properties"]["Environment"]["AgentCoreRuntimeEnvironment"]["NetworkConfiguration"]


def s3_statement(t):
    return res(t, "EndpointS3")["Properties"]["PolicyDocument"]["Statement"][0]


def interface(t, name, service):
    add(t, name, "AWS::EC2::VPCEndpoint", {"VpcId": {"Ref": "Vpc"}, "VpcEndpointType": "Interface",
                                           "ServiceName": service,
                                           "SubnetIds": [{"Ref": "AgentSubnetA"}],
                                           "SecurityGroupIds": [{"Ref": "EndpointSecurityGroup"}]})


def allowed_domains(t):
    return res(t, "DnsAllowed")["Properties"]["Domains"]


CASES = [
    ("the template as committed", lambda t: None, 0, "ok "),
    ("a NAT route from the agent subnets",
     lambda t: add(t, "Nat", "AWS::EC2::Route", {"RouteTableId": {"Ref": "AgentRouteTable"},
                                                 "DestinationCidrBlock": "0.0.0.0/0",
                                                 "NatGatewayId": "nat-0123"}),
     1, "route Nat sends 0.0.0.0/0"),
    ("an IPv6 default route, behind a condition",
     lambda t: add(t, "Six", "AWS::EC2::Route", {"RouteTableId": {"Ref": "AgentRouteTable"},
                                                 "DestinationIpv6CidrBlock": "::/0",
                                                 "EgressOnlyInternetGatewayId": "eigw-0123"},
                   Condition="IsHarness"),
     1, "route Six sends ::/0"),
    ("an agent subnet on the service route table",
     lambda t: res(t, "AgentSubnetARoutes")["Properties"].update(RouteTableId={"Ref": "ServiceRouteTable"}),
     1, "route ServiceDefaultRoute sends 0.0.0.0/0"),
    ("an agent subnet with no route table association",
     lambda t: t["Resources"].pop("AgentSubnetBRoutes"),
     1, "agent subnet AgentSubnetB has no route table association"),
    ("a DynamoDB gateway endpoint on the agent route table",
     lambda t: add(t, "Dynamo", "AWS::EC2::VPCEndpoint", {"VpcId": {"Ref": "Vpc"}, "VpcEndpointType": "Gateway",
                                                         "ServiceName": {"Fn::Sub": "com.amazonaws.${AWS::Region}.dynamodb"},
                                                         "RouteTableIds": [{"Ref": "AgentRouteTable"}]}),
     1, "gateway endpoint Dynamo"),
    ("no inline egress on the agent group",
     lambda t: res(t, "AgentSecurityGroup")["Properties"].pop("SecurityGroupEgress"),
     1, "lists no egress inline"),
    ("egress to the internet",
     lambda t: add(t, "Out", "AWS::EC2::SecurityGroupEgress", {"GroupId": {"Ref": "AgentSecurityGroup"},
                                                              "IpProtocol": "tcp", "FromPort": 443, "ToPort": 443,
                                                              "CidrIp": "0.0.0.0/0"}),
     1, "Out: egress to 0.0.0.0/0"),
    ("every protocol to the sidecar",
     lambda t: res(t, "AgentToSidecar")["Properties"].update(IpProtocol="-1"),
     1, "AgentToSidecar: protocol -1"),
    ("egress to another group",
     lambda t: res(t, "AgentToSidecar")["Properties"].update(
         DestinationSecurityGroupId={"Ref": "EndpointSecurityGroup"}, FromPort=22, ToPort=22),
     1, "AgentToSidecar: ports (22, 22)"),
    ("egress to a group outside the template",
     lambda t: res(t, "AgentToSidecar")["Properties"].update(DestinationSecurityGroupId="sg-0123"),
     1, "AgentToSidecar: egress to sg-0123"),
    ("a conditional agent group",
     lambda t: res(t, "AgentSecurityGroup").update(Condition="IsHarness"),
     1, "AgentSecurityGroup carries Condition"),
    ("the default S3 endpoint policy",
     lambda t: res(t, "EndpointS3")["Properties"].pop("PolicyDocument"),
     1, "EndpointS3 has no PolicyDocument"),
    ("s3:* on the layer bucket",
     lambda t: s3_statement(t).update(Action="s3:*"),
     1, "allows s3:*"),
    ("s3:PutObject next to s3:GetObject",
     lambda t: s3_statement(t).update(Action=["s3:GetObject", "s3:PutObject"]),
     1, "not only s3:GetObject"),
    ("another bucket",
     lambda t: res(t, "EndpointS3")["Properties"]["PolicyDocument"]["Statement"].append(
         {"Effect": "Allow", "Principal": "*", "Action": "s3:GetObject", "Resource": "arn:aws:s3:::outside/*"}),
     1, "not only the ECR layer bucket"),
    ("a look-alike layer bucket",
     lambda t: s3_statement(t).update(Resource="arn:aws:s3:::prod-evil-starport-layer-bucket/*"),
     1, "not only the ECR layer bucket"),
    ("NotResource",
     lambda t: s3_statement(t).update(NotResource="arn:aws:s3:::nothing/*"),
     1, "uses a Not* element"),
    ("no DNS Firewall association",
     lambda t: t["Resources"].pop("DnsFirewallAssociation"),
     1, "VPC Vpc has no Route 53 Resolver DNS Firewall association"),
    ("a conditional DNS Firewall association",
     lambda t: res(t, "DnsFirewallAssociation").update(Condition="IsHarness"),
     1, "DnsFirewallAssociation carries Condition"),
    ("no block rule",
     lambda t: res(t, "DnsFirewall")["Properties"]["FirewallRules"].pop(1),
     1, "does not block '*.'"),
    ("the block rule before the allow rule",
     lambda t: res(t, "DnsFirewall")["Properties"]["FirewallRules"][1].update(Priority=50),
     1, "does not block '*.'"),
    ("a harness on the public network",
     lambda t: harness_net(t).update(NetworkMode="PUBLIC"),
     1, "Harness runs with NetworkMode PUBLIC"),
    ("a harness in a subnet the outputs do not name",
     lambda t: harness_net(t)["NetworkModeConfig"].update(Subnets=[{"Ref": "ServiceSubnet"}]),
     1, "Harness Subnets"),
    ("no AgentSecurityGroupId output",
     lambda t: t["Outputs"].pop("AgentSecurityGroupId"),
     1, "no AgentSecurityGroupId output"),
    ("a gateway endpoint with the default type",
     lambda t: add(t, "Dynamo", "AWS::EC2::VPCEndpoint", {"VpcId": {"Ref": "Vpc"},
                                                         "ServiceName": {"Fn::Sub": "com.amazonaws.${AWS::Region}.dynamodb"},
                                                         "RouteTableIds": [{"Ref": "AgentRouteTable"}]}),
     1, "gateway endpoint Dynamo"),
    ("a route table named by an expression",
     lambda t: add(t, "Maybe", "AWS::EC2::Route", {"RouteTableId": {"Fn::If": ["IsHarness", {"Ref": "AgentRouteTable"},
                                                                               {"Ref": "ServiceRouteTable"}]},
                                                   "DestinationCidrBlock": "0.0.0.0/0", "GatewayId": "igw-0123"}),
     1, "route Maybe names its route table by an expression"),
    ("VPN routes propagated into the agent route table",
     lambda t: add(t, "Vpn", "AWS::EC2::VPNGatewayRoutePropagation", {"RouteTableIds": [{"Ref": "AgentRouteTable"}],
                                                                      "VpnGatewayId": "vgw-0123"}),
     1, "Vpn propagates routes"),
    ("an S3 interface endpoint with the default policy",
     lambda t: interface(t, "S3Iface", {"Fn::Sub": "com.amazonaws.${AWS::Region}.s3"}),
     1, "interface endpoint S3Iface"),
    ("an outside party's PrivateLink service",
     lambda t: interface(t, "Outside", "com.amazonaws.vpce.us-east-1.vpce-svc-0123456789abcdef0"),
     1, "interface endpoint Outside"),
    ("an STS endpoint",
     lambda t: interface(t, "Sts", {"Fn::Sub": "com.amazonaws.${AWS::Region}.sts"}),
     1, "interface endpoint Sts"),
    ("egress to another prefix list",
     lambda t: res(t, "AgentSecurityGroup")["Properties"]["SecurityGroupEgress"][1].update(
         DestinationPrefixListId="pl-0123456789abcdef0"),
     1, "egress to prefix list pl-0123456789abcdef0"),
    ("every port to the sidecar",
     lambda t: res(t, "AgentToSidecar")["Properties"].update(FromPort=0, ToPort=65535),
     1, "AgentToSidecar: ports (0, 65535) to the sidecar"),
    ("TCP to the sidecar with no ports",
     lambda t: [res(t, "AgentToSidecar")["Properties"].pop(k) for k in ("FromPort", "ToPort")],
     1, "AgentToSidecar: ports (None, None) to the sidecar"),
    ("the sidecar open to the internet",
     lambda t: add(t, "Open", "AWS::EC2::SecurityGroupIngress", {"GroupId": {"Ref": "SidecarSecurityGroup"},
                                                                "IpProtocol": "tcp", "FromPort": 8080,
                                                                "ToPort": 8080, "CidrIp": "0.0.0.0/0"}),
     1, "Open: ingress to the sidecar from 0.0.0.0/0"),
    ("a proxy behind the sidecar's group",
     lambda t: (add(t, "ProxyTask", "AWS::ECS::TaskDefinition", {"ContainerDefinitions": [{"Name": "squid",
                                                                                          "Image": "ubuntu/squid"}]}),
                add(t, "Proxy", "AWS::ECS::Service", {"TaskDefinition": {"Ref": "ProxyTask"}, "NetworkConfiguration": {
                    "AwsvpcConfiguration": {"SecurityGroups": [{"Ref": "SidecarSecurityGroup"}]}}})),
     1, "Proxy runs ubuntu/squid"),
    ("an instance in the endpoints' group",
     lambda t: add(t, "Box", "AWS::EC2::Instance", {"SecurityGroupIds": [{"Ref": "EndpointSecurityGroup"}]}),
     1, "Box (AWS::EC2::Instance) carries EndpointSecurityGroup"),
    ("every name allowed",
     lambda t: allowed_domains(t).append("*."),
     1, "DnsAllowed allows '*.'"),
    ("every .com name allowed",
     lambda t: allowed_domains(t).append("*.com."),
     1, "DnsAllowed allows '*.com.'"),
    ("an allow rule on a list outside the template",
     lambda t: res(t, "DnsFirewall")["Properties"]["FirewallRules"].append(
         {"FirewallDomainListId": "rslvr-fdl-0123", "Priority": 150, "Action": "ALLOW"}),
     1, "allows domain list rslvr-fdl-0123"),
    ("an endpoint with the default policy",
     lambda t: res(t, "EndpointLogs")["Properties"].pop("PolicyDocument"),
     1, "interface endpoint EndpointLogs admits any account's keys"),
    ("an endpoint policy without the account condition",
     lambda t: res(t, "EndpointEcrDkr")["Properties"]["PolicyDocument"]["Statement"][0].pop("Condition"),
     1, "interface endpoint EndpointEcrDkr admits any account's keys"),
    ("agent subnets named by literal ids",
     lambda t: (t["Outputs"]["AgentSubnetIds"].update(Value="subnet-0123,subnet-4567"),
                harness_net(t)["NetworkModeConfig"].update(Subnets=["subnet-0123", "subnet-4567"])),
     1, "an agent subnet is named by a literal id"),
    ("a block rule for A queries only",
     lambda t: res(t, "DnsFirewall")["Properties"]["FirewallRules"][1].update(Qtype="A"),
     1, "does not block '*.'"),
    ("an allow entry that a condition turns into '*.'",
     lambda t: allowed_domains(t).append({"Fn::If": ["IsHarness", "*.", "api.example.com."]}),
     1, "a condition or a wildcard in an expression"),
    ("an allow entry that a condition drops for '*.'",
     lambda t: allowed_domains(t).append({"Fn::If": ["IsDemo", {"Ref": "AWS::NoValue"}, "*."]}),
     1, "a condition or a wildcard in an expression"),
    ("a condition that drops a choice between two entries",
     lambda t: allowed_domains(t).append({"Fn::If": ["IsDemo", {"Ref": "AWS::NoValue"},
                                                     {"Fn::If": ["IsHarness", "*.", "api.example.com."]}]}),
     1, "a condition or a wildcard in an expression"),
    ("a wildcard inside an expression",
     lambda t: allowed_domains(t).append({"Fn::Sub": "*.${AWS::Region}."}),
     1, "a condition or a wildcard in an expression"),
    ("an agent subnet whose route table is picked by a condition",
     lambda t: res(t, "AgentSubnetARoutes")["Properties"].update(
         RouteTableId={"Fn::If": ["IsHarness", {"Ref": "AgentRouteTable"}, {"Ref": "ServiceRouteTable"}]}),
     1, "AgentSubnetARoutes names its RouteTableId by an expression"),
    ("an egress rule whose group is picked by a condition",
     lambda t: add(t, "Sneaky", "AWS::EC2::SecurityGroupEgress", {
         "GroupId": {"Fn::If": ["IsHarness", {"Ref": "AgentSecurityGroup"}, {"Ref": "SidecarSecurityGroup"}]},
         "IpProtocol": "-1", "CidrIp": "0.0.0.0/0"}),
     1, "Sneaky names its GroupId by an expression"),
    ("a sidecar task picked by a condition",
     lambda t: res(t, "SidecarService")["Properties"].update(
         TaskDefinition={"Fn::If": ["IsHarness", {"Ref": "SidecarTask"}, "arn:aws:ecs:us-east-1:111122223333:task-definition/relay:1"]}),
     1, "only a task definition in this template can be checked"),
    ("a sidecar task from outside the template",
     lambda t: res(t, "SidecarService")["Properties"].update(TaskDefinition="relay:1"),
     1, "only a task definition in this template can be checked"),
    ("a sidecar only for the harness",
     lambda t: res(t, "SidecarService").update(Condition="IsHarness"),
     1, "SidecarService carries Condition"),
    ("an image parameter with no pattern",
     lambda t: t["Parameters"]["SidecarImage"].pop("AllowedPattern"),
     1, "needs an AllowedPattern"),
    ("the distroless image, which has no bash",
     lambda t: t["Parameters"]["SidecarImage"].update(Default="hoophq/hoopsidecar:1.272.0-distroless"),
     1, "needs an AllowedPattern"),
    ("a Resolver rule that forwards names",
     lambda t: add(t, "Forward", "AWS::Route53Resolver::ResolverRuleAssociation", {"ResolverRuleId": "rslvr-rr-0123",
                                                                                   "VPCId": {"Ref": "Vpc"}}),
     1, "Forward attaches a Resolver rule"),
]


def main():
    # A misspelled type matches no resource, and its rule passes forever.
    known = set(PROVIDER_SCHEMA_MANAGER.get_resource_types("us-east-1"))
    with open(CHECK) as fp:
        unknown = sorted(set(re.findall(r"AWS::\w+::\w+", fp.read())) - known)
    if unknown:
        print(f"FAIL check-agentcore-egress.py names types CloudFormation does not have: {unknown}")
        return 1
    base, matches = decode(TEMPLATE)
    if base is None:
        print(f"FAIL cannot read {TEMPLATE}: {matches}")
        return 1
    passed = failed = 0
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, mutate, want_code, want_text) in enumerate(CASES):
            t = copy.deepcopy(base)
            mutate(t)
            path = os.path.join(tmp, f"case{i}.json")
            with open(path, "w") as fp:
                json.dump(t, fp)
            run = subprocess.run([sys.executable, CHECK, path], capture_output=True, text=True)
            if run.returncode == want_code and want_text in run.stdout:
                passed += 1
            else:
                failed += 1
                print(f"FAIL {name}: exit {run.returncode}, want {want_code} and {want_text!r}\n"
                      f"{run.stdout}{run.stderr}")
    print(f"{passed} passed, {failed} failed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
