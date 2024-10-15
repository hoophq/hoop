![hero](github.png)

<h1 align="center"><b>hoop.dev</b></h1>
<p align="center">
    🔒 Secure, seamless access to databases and servers. No compromises.
    <br />
    <br />
    <a target="_blank" href="https://hoop.dev">Website</a>
    ·
    <a target="_blank" href="https://hoop.dev/docs">Docs</a>
    ·
    <a href="https://github.com/hoophq/hoop/discussions">Discussions</a>
  </p>
</p>


<p align="center">
    <a href="https://github.com/hoophq/hoop/actions/workflows/release.yml">
        <img src="https://img.shields.io/github/v/release/hoophq/hoop.svg?style=flat" />
    </a>
    <a href="https://github.com/hoophq/hoop/actions/workflows/release.yml">
        <img src="https://github.com/hoophq/hoop/actions/workflows/release.yml/badge.svg" />
    </a>
</p>

## Features

- 🔐 **Zero Trust Access**: Connect securely without VPNs or exposed credentials
- 🛡️ **Real-time Data Masking**: Automatically hide sensitive data in transit
- 🛠 **Granular Access Control**: Just-in-Time, least-privilege access to resources
- 🌐 **Audit Logging**: Comprehensive logs of all actions and queries
- 🤖 **ChatOps Integration**: Approve access requests via Slack or MS Teams
- ☁️ **Multi-Cloud Support**: Works with AWS, GCP, Azure, and on-premises setups

## 🌟 Why Hoop?

- **Simplified Access Management**: No more VPN or SSH key nightmares
- **Enhanced Security**: Reduce attack surface and prevent credential leaks
- **Compliance Made Easy**: Meet SOC2, HIPAA, and GDPR requirements out of the box
- **Developer Productivity**: Faster, safer access to the resources devs need

<!--
## 🚀 Quick Start

Get up and running in minutes:

```bash
# create a jwt secret for auth
echo "JWT_SECRET_KEY=$(openssl rand -hex 32)" >> .env

# download and run
curl -sL https://hoop.dev/docker-compose.yml > docker-compose.yml | docker compose -f - up

```
[View full installation options](#installation)
-->
## 📚 Popular Guides

- [Secure MySQL Access](https://hoop.dev/docs/quickstarts/mysql)
- [Kubernetes Integration](https://hoop.dev/docs/quickstarts/kubernetes)
- [AI-Powered Data Masking](https://hoop.dev/docs/learn/ai-data-masking)
- [Implement Just-in-Time Reviews](https://hoop.dev/docs/learn/jit-reviews)

[Explore all guides](#guides)

## 🌟 Key Features

- [AI Data Masking](https://hoop.dev/docs/learn/ai-data-masking)
- [Granular Access Control](https://hoop.dev/docs/learn/access-control)
- [Just-in-Time Reviews](https://hoop.dev/docs/learn/jit-reviews)
- [Automated Runbooks](https://hoop.dev/docs/learn/runbooks)
- [Secrets Manager Integration](https://hoop.dev/docs/learn/secrets-manager)
- [Comprehensive Session Recording](https://hoop.dev/docs/learn/session-recording)
- [Webhooks/SIEM Support](https://hoop.dev/docs/learn/webhooks-siem)
- [AI Query Builder](https://hoop.dev/docs/learn/ai-query-builder)

[Explore features](#features)


## About hoop.dev

Hoop.dev is an access gateway for databases and servers with an API for packet manipulation. Because of the modern architecture powering Hoop, the open-source version includes advanced features like:

 * **Passwordless Auth, No Certificates**: older gateways require high-maintenance certificate authorities. Hoop uses OIDC and Oauth2 for authentication, letting your IDP handle everything behind the scenes. Forget about certificates!
 * **Open-source SSO**: support for Okta, Keycloak, Jumpcloud, and others. There is no need for Enterprise versions to integrate your own IDP. You're not limited to GitHub sign-in.
 * **Session recording**: Linux, Docker, Kubernetes, Mysql, Postgres, MongoDB, and many more.
 * **Just-in-time access**: grant time-bound sessions using groups synced from your IDP.
 * **Slack and Teams Access Requests**: Chatbot approval workflows can be done without leaving your chat app.

Discover the unique capabilities that only Hoop can offer. From packet manipulation to web and proxy modes, Hoop is designed to meet your diverse needs.

* **Manipulate packets**: Programmatically changes the gateway's environment and each connection's packets in real-time. Check out the [Secrets Manager integration example](https://hoop.dev/docs/learn/secrets-manager).
 * **Web and proxy modes**: Existing gateways lock you into either a web client interface or a proxy that requires desktop agents. Hoop gives you both options.
 * **Custom connections**: bring your own CLI or hide complex options from developers.

See the full list of features for the free open-source and the enterprise versions on [hoop.dev/features](https://hoop.dev/features).

## Installation

### Docker

[See Docker Compose installation documentation](https://hoop.dev/docs/getting-started/installation/docker-compose)
<!--
```bash
# create a jwt secret for auth
echo "JWT_SECRET_KEY=$(openssl rand -hex 32)" >> .env

# download and run
curl -sL https://hoop.dev/docker-compose.yml > docker-compose.yml | docker compose -f - up

-->
### Kubernetes

[See Kubernetes Deployment Documentation](https://hoop.dev/docs/deploy/kubernetes)

### AWS

 [See AWS Deploy & Host Documentation](https://hoop.dev/docs/deploy/AWS)

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

<!--
## 🚀 Quick Start

Get up and running in minutes:

```bash
curl -sL https://hoop.dev/install.sh | sh
```

[View full installation options](#installation)
-->
## Guides

### Databases
- [MySQL](https://hoop.dev/docs/quickstarts/mysql)
- [PostgreSQL](https://hoop.dev/docs/quickstarts/postgres)
- [MongoDB](https://hoop.dev/docs/quickstarts/mongodb)
- [MSSQL](https://hoop.dev/docs/quickstarts/mssql)
- [Oracle](https://hoop.dev/docs/quickstarts/oracle)
- [Apache Cassandra](https://hoop.dev/docs/quickstarts/apache-cassandra)

### Cloud & Infrastructure
- [Kubernetes](https://hoop.dev/docs/quickstarts/kubernetes)
- [AWS](https://hoop.dev/docs/quickstarts/aws)
- [SSH Jump Hosts](https://hoop.dev/docs/quickstarts/ssh-jump-hosts)

### Application Consoles
- [Ruby on Rails Console](https://hoop.dev/docs/quickstarts/ruby-on-rails)
- [Elixir IEx](https://hoop.dev/docs/quickstarts/elixir-IEx)
- [PHP Artisan](https://hoop.dev/docs/quickstarts/php-artisan)
- [Python Environments](https://hoop.dev/docs/quickstarts/python)

### Web & APIs
- [Web Apps & APIs](https://hoop.dev/docs/quickstarts/webapps-and-apis)

[Explore all guides](https://hoop.dev/docs/quickstarts)

## Features

- [AI Data Masking](https://hoop.dev/docs/learn/ai-data-masking)
- [Access Control](https://hoop.dev/docs/learn/access-control)
- [Just-in-Time Reviews](https://hoop.dev/docs/learn/jit-reviews)
- [Runbooks](https://hoop.dev/docs/learn/runbooks)
- [Secrets Manager](https://hoop.dev/docs/learn/secrets-manager)
- [Session Recording](https://hoop.dev/docs/learn/session-recording)
- [Webhooks/SIEM](https://hoop.dev/docs/learn/webhooks-siem)
- [AI Query Builder](https://hoop.dev/docs/learn/ai-query-builder)

[See all features](https://hoop.dev/features)

## 🤝 Contributing

We welcome contributions! Check out our [Development Documentation](/DEV.md) to get started.

## 📣 Community

Join our [Discussions](https://github.com/hoophq/hoop/discussions) to ask questions, share ideas, and connect with other users.

## Backed by

![Backed By YC, Valor, GFC, Quiet and L2 Ventures](backedby.png)
