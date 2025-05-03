
![hero](github.png)

<h1 align="center">
<b>hoop.dev</b>
</h1>
<p align="center"> 🔒 Secure infrastructure access without complexity or cost 
<br /> <br />
 <a target="_blank" href="https://hoop.dev">Website</a> · <a target="_blank" href="https://hoop.dev/docs">Docs</a> · <a href="https://github.com/hoophq/hoop/discussions">Discussions</a> </p> </p>
 <p align="center"><a href="https://github.com/hoophq/hoop/actions/workflows/release.yml"><img src="https://img.shields.io/github/v/release/hoophq/hoop.svg?style=flat" /> </a><img src="https://img.shields.io/badge/Setup-4.3_min-success" /></p>

Hoop.dev is the free, open-source access gateway for databases and servers - the secure alternative to VPNs, credential sharing, and access tickets.

## What is hoop.dev?

Hoop is a **proxy** that secures and simplifies access to your infrastructure. It acts as an intelligent pipeline between your team and your resources (databases, servers, Kubernetes):

-   **No VPNs or exposed credentials**  - Outbound-only connections with zero inbound firewall rules
-   **Free SSO integration**  - Works with Google, Okta, JumpCloud, and more with no additional fees
-   **Complete audit trail**  - Every action is recorded in a standardized format for compliance
-   **Deploy in minutes**  - Average setup time of 4.3 minutes across 200+ deployments

## 🚀 Quick Start

Get up and running in minutes:

bash

```bash
# create a jwt secret for auth
echo "JWT_SECRET_KEY=$(openssl rand -hex 32)" >> .env

# download and run
curl -sL https://hoop.dev/docker-compose.yml > docker-compose.yml
docker compose up
```

[View full installation options](https://claude.ai/chat/cd0e4113-01b1-47b0-8d1a-6eec819a3f07#installation)

## How hoop.dev Works

Show Image

Hoop creates a secure **pipe** between users and infrastructure:

1.  **Authentication**  - Users authenticate through your existing identity provider (Google, Okta, etc.)
2.  **Connection**  - Hoop agents establish outbound-only connections to your resources
3.  **Access**  - Users connect through the Hoop proxy with just-in-time permissions
4.  **Audit**  - Every action is recorded for complete visibility and compliance

## Why Use hoop.dev?

### ⚡ Eliminate Security Vulnerabilities

VPNs and public endpoints create unnecessary attack vectors. Hoop agents establish protected outbound-only connections between authenticated users and your authorized resources—no inbound traffic required. This reduces your attack surface while simplifying your network architecture, minimizing time spent managing complex firewall rules.

### 💸 End the SSO Tax

Enterprise tools charge substantial fees annually just to connect your identity provider. Hoop integrates freely with Google Workspaces, Okta, JumpCloud, Entra ID, Auth0, and AWS Cognito—with no additional licensing fees. Save on costs while improving security through unified authentication without the SSO tax that other solutions impose.

### 🔑 Automate Access Controls

Stop spending hours processing access request tickets. Hoop automatically maps your existing identity provider groups to read-only, read-write, or admin profiles across all your infrastructure. Delegate access management to IT using your existing group structure and free up engineering time for higher-value tasks.

### 📊 Standardize Audit Trails

Multiple audit formats across different systems create compliance challenges. Hoop records every action in a single, standardized format across all your infrastructure—from database queries to Kubernetes commands. Transform audit preparation from a time-consuming project to a streamlined process while maintaining compliance with SOC2, GDPR, and other frameworks.

## 📚 Popular Guides

### Databases

-   [MySQL](https://hoop.dev/docs/quickstart/databases/mysql)
-   [PostgreSQL](https://hoop.dev/docs/quickstart/databases/postgres)
-   [MongoDB](https://hoop.dev/docs/quickstart/databases/mongodb)
-   [MSSQL](https://hoop.dev/docs/quickstart/databases/mssql)

### Cloud & Infrastructure

-   [Kubernetes](https://hoop.dev/docs/quickstart/cloud-services/kubernetes)
-   [AWS](https://hoop.dev/docs/quickstart/cloud-services/aws/aws-cli)
-   [SSH Jump Hosts](https://hoop.dev/docs/quickstart/web-applications/jump-hosts)

[View all guides](https://hoop.dev/docs/quickstart)

## Installation

### Docker

bash

```bash
# create a jwt secret for auth
echo "JWT_SECRET_KEY=$(openssl rand -hex 32)" >> .env

# download and run
curl  -sL https://hoop.dev/docker-compose.yml > docker-compose.yml &&  docker compose up
```

[See Docker Compose installation documentation](https://hoop.dev/docs/setup/deployment/docker-compose)

### Kubernetes

[See Kubernetes Deployment Documentation](https://hoop.dev/docs/setup/deployment/kubernetes)

### AWS

[See AWS Deploy & Host Documentation](https://hoop.dev/docs/setup/deployment/AWS)

| Region | Launch Stack |
|--------|--------------|
| N. Virginia (us-east-1) | [![Launch Stack](https://cdn.rawgit.com/buildkite/cloudformation-launch-stack-button-svg/master/launch-stack.svg)](https://us-east-1.console.aws.amazon.com/cloudformation/home?region=us-east-1#/stacks/quickcreate?templateURL=https%3A%2F%2Fhoopdev-platform-cf-us-east-1.s3.us-east-1.amazonaws.com%2Flatest%2Fhoopdev-platform.template.yaml) |
| Ohio (us-east-2) | [![Launch Stack](https://cdn.rawgit.com/buildkite/cloudformation-launch-stack-button-svg/master/launch-stack.svg)](https://us-east-2.console.aws.amazon.com/cloudformation/home?region=us-east-2#/stacks/quickcreate?templateURL=https%3A%2F%2Fhoopdev-platform-cf-us-east-2.s3.us-east-2.amazonaws.com%2Flatest%2Fhoopdev-platform.template.yaml) |
| N. California (us-west-1) | [![Launch Stack](https://cdn.rawgit.com/buildkite/cloudformation-launch-stack-button-svg/master/launch-stack.svg)](https://us-west-1.console.aws.amazon.com/cloudformation/home?region=us-west-1#/stacks/quickcreate?templateURL=https%3A%2F%2Fhoopdev-platform-cf-us-west-1.s3.us-west-1.amazonaws.com%2Flatest%2Fhoopdev-platform.template.yaml) |
| Oregon (us-west-2) | [![Launch Stack](https://cdn.rawgit.com/buildkite/cloudformation-launch-stack-button-svg/master/launch-stack.svg)](https://us-west-2.console.aws.amazon.com/cloudformation/home?region=us-west-2#/stacks/quickcreate?templateURL=https%3A%2F%2Fhoopdev-platform-cf-us-west-2.s3.us-west-2.amazonaws.com%2Flatest%2Fhoopdev-platform.template.yaml) |
| Ireland (eu-west-1) | [![Launch Stack](https://cdn.rawgit.com/buildkite/cloudformation-launch-stack-button-svg/master/launch-stack.svg)](https://eu-west-1.console.aws.amazon.com/cloudformation/home?region=eu-west-1#/stacks/quickcreate?templateURL=https%3A%2F%2Fhoopdev-platform-cf-eu-west-1.s3.eu-west-1.amazonaws.com%2Flatest%2Fhoopdev-platform.template.yaml) |
| London (eu-west-2) | [![Launch Stack](https://cdn.rawgit.com/buildkite/cloudformation-launch-stack-button-svg/master/launch-stack.svg)](https://eu-west-2.console.aws.amazon.com/cloudformation/home?region=eu-west-2#/stacks/quickcreate?templateURL=https%3A%2F%2Fhoopdev-platform-cf-eu-west-2.s3.eu-west-2.amazonaws.com%2Flatest%2Fhoopdev-platform.template.yaml) |
| Frankfurt (eu-central-1) | [![Launch Stack](https://cdn.rawgit.com/buildkite/cloudformation-launch-stack-button-svg/master/launch-stack.svg)](https://eu-central-1.console.aws.amazon.com/cloudformation/home?region=eu-central-1#/stacks/quickcreate?templateURL=https%3A%2F%2Fhoopdev-platform-cf-eu-central-1.s3.eu-central-1.amazonaws.com%2Flatest%2Fhoopdev-platform.template.yaml) |
| Sydney (ap-southeast-2) | [![Launch Stack](https://cdn.rawgit.com/buildkite/cloudformation-launch-stack-button-svg/master/launch-stack.svg)](https://ap-southeast-2.console.aws.amazon.com/cloudformation/home?region=ap-southeast-2#/stacks/quickcreate?templateURL=https%3A%2F%2Fhoopdev-platform-cf-ap-southeast-2.s3.ap-southeast-2.amazonaws.com%2Flatest%2Fhoopdev-platform.template.yaml) |

[View all regions](https://hoop.dev/docs/deploy/AWS)

## Advanced Features

What makes Hoop unique is its ability to not only inspect but also modify connections between users and infrastructure:

-   [**AI Data Masking**](https://hoop.dev/docs/learn/features/ai-data-masking)  - Automatically hide sensitive data like emails, SSNs, and credit cards
-   [**Just-in-Time Reviews**](https://hoop.dev/docs/learn/features/reviews/overview)  - Approve risky commands in real-time through Slack or MS Teams
-   [**Runbooks**](https://hoop.dev/docs/learn/features/runbooks)  - Create pre-approved workflows for common tasks
-   [**Web & Native Modes**](https://hoop.dev/docs/clients)  - Use the web interface or connect through your native database tools

[See all features](https://hoop.dev/docs/learn/features)

## You'll be in Good Company

-   **200+ successful deployments**  from companies around the world
-   **4.3 minute average setup time**  across all deployments
-   **Trusted by teams**  from startups to enterprises

## 🤝 Contributing

We welcome contributions! Check out our [Development Documentation](/DEV.md) to get started.

## 📣 Community

Join our [Discussions](https://github.com/hoophq/hoop/discussions) to ask questions, share ideas, and connect with other users.

## Backed by

![Backed By YC, Valor, GFC, Quiet and L2 Ventures](backedby.png)
