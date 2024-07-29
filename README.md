![hero](github.png)

<h1 align="center"><b>hoop.dev</b></h1>
<p align="center">
    Access databases and servers with zero security compromises
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


## About hoop.dev

Hoop.dev is an access gateway for databases and servers with an API for packet manipulation. Because of the modern architecture powering Hoop, the open-source version includes advanced features like:

 * **Passwordless Auth, No Certificates**: older gateways require high-maintenance certificate authorities. Hoop uses OIDC and Oauth2 for authentication, letting your IDP handle everything behind the scenes. Forget about certificates!
 * **Open-source SSO**: support for Okta, Keycloak, Jumpcloud, and others. There is no need for Enterprise versions to integrate your own IDP. You're not limited to GitHub sign-in.
 * **Session recording**: Linux, Docker, Kubernetes, Mysql, Postgres, MongoDB, and many more.
 * **Just-in-time access**: grant time-bound sessions using groups synced from your IDP.
 * **Slack and Teams Access Requests**: Chatbot approval workflows can be done without leaving your chat app.

Discover the unique capabilities that only Hoop can offer. From packet manipulation to web and proxy modes, Hoop is designed to meet your diverse needs.

* **Manipulate packets**: Programmatically changes the gateway's environment and each connection's packets in real-time. Check out the [Secrets Manager integration example](https://hoop.dev/docs/learn/secrets-manager).
 * **Web and proxy modes**: Existing gateways lock you into either a web client interface or a proxy that requires desktop agents. Hoop gives you both options.
 * **Custom connections**: bring your own CLI or hide complex options from developers.

See the full list of features for the free open-source and the enterprise versions on [hoop.dev/features](https://hoop.dev/features).

## Installation

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

## Development

Check out our [Development Documentation](/DEV.md)

## Backed by

![Backed By YC, Valor, GFC, Quiet and L2 Ventures](backedby.png)
