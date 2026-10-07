# Security Policy

**Found a vulnerability in Hoop? Report it privately. Never in a public issue, pull request, or discussion.**

Hoop sits between people, AI agents, and production infrastructure. A security bug here can expose databases, servers, and clusters. We treat every report as a priority, and we will work with you to fix and disclose it responsibly.

## Report a vulnerability

Use one of these private channels:

1. **GitHub private vulnerability reporting (preferred).** Go to the [Security tab](https://github.com/hoophq/hoop/security) of this repository and click **Report a vulnerability**. Only Hoop maintainers can see your report.
2. **Email:** [security@hoop.dev](mailto:security@hoop.dev).

**Please include:**

- The affected component (gateway, agent, CLI, web app, Helm chart, or container image) and version
- A description of the vulnerability and its impact
- Step-by-step instructions to reproduce it, with a proof of concept if you have one
- Any configuration needed to trigger it, such as authentication mode or enabled plugins
- How you would like to be credited, if at all

One clear report beats many partial ones. If you find several related issues, send them together.

## What to expect

| Step | Target |
| --- | --- |
| We acknowledge your report | Within 2 business days |
| We confirm the issue and share an initial severity | Within 5 business days |
| We send status updates | At least every 7 days until it's fixed |

We rate severity with [CVSS v3.1](https://www.first.org/cvss/v3.1/specification-document). Our remediation targets after we confirm an issue:

| Severity | Target fix time |
| --- | --- |
| Critical | 7 days |
| High | 30 days |
| Medium | 90 days |
| Low | Next planned release |

Complex fixes can take longer. If that happens, we will tell you why and agree on a new timeline with you.

## Coordinated disclosure

We follow coordinated disclosure:

- **Keep the details private** until we release a fix, or for 90 days after your report, whichever comes first. We may ask to extend this window for a complex fix, and we will explain why.
- **We publish a GitHub Security Advisory** for each confirmed vulnerability once a fix is available. It includes affected versions, fixed versions, and upgrade steps.
- **We request a CVE** for confirmed vulnerabilities in released versions.
- **We credit you** in the advisory, unless you prefer to stay anonymous.

## Supported versions

We release security fixes for the **latest release** of Hoop. Older versions do not receive patches. Run the latest release to stay protected.

Most Hoop deployments are self-hosted, so you control when you upgrade. Watch this repository's releases and security advisories to hear about fixes as soon as they ship. Upgrade steps are in our [documentation](https://hoop.dev/docs).

## Scope

**In scope:**

- Code in this repository: the gateway, agent, CLI, and web app
- Official container images and Helm charts published by Hoop
- The hoop.dev managed service

**Out of scope:**

- Vulnerabilities in third-party dependencies with no demonstrated impact on Hoop. Report those upstream, and tell us if they affect Hoop.
- Issues caused by insecure configuration of a self-hosted deployment, unless the default configuration is insecure
- Findings that require an already compromised admin account, host, or identity provider
- Volumetric denial of service attacks
- Social engineering, phishing, or physical attacks on Hoop employees or offices
- Automated scanner output without a working proof of concept
- Missing security headers or best-practice suggestions with no exploitable impact

Not sure if your finding is in scope? Report it anyway. We would rather review it than miss it.

## Safe harbor

We will not take legal action against researchers who act in good faith under this policy. Good faith means you:

- Test only against your own deployment or accounts you own, never against other customers' data
- Stop testing and report right away once you confirm a vulnerability
- Do not access, change, or delete data that is not yours, and do not keep any data you see
- Do not degrade or disrupt service for others
- Give us a reasonable time to fix the issue before sharing details publicly

If a third party takes legal action against you for research done under this policy, we will make it known that your work was authorized.

We do not run a paid bug bounty program at this time.

## Other security questions

Hoop holds a SOC 2 Type II report. For our report, security questionnaires, or other security questions that are not vulnerability reports, contact [security@hoop.dev](mailto:security@hoop.dev).
