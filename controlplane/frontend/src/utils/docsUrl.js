// Documentation links.
//
// This started as a mirror of webapp/src/webapp/config.cljs. It is not one any more:
// that file belongs to the gateway's UI, and keeping the two in step would keep pulling
// gateway vocabulary in here. Add and remove entries as this app needs them.
//
// A NEW link comes from the Sidecar / Control Plane trunk — the five sections listed in
// controlplane/PRODUCT.md. Everything under /learn/, /clients/ and /quickstart/
// documents the Hoop Gateway, a different product; those entries are here because the
// screens that use them are the gateway's too. See CLAUDE.md.
//
// The docs site serves NO 404: an unknown path silently returns the docs home page. So
// a link check on the HTTP status passes on a dead link. Verify the body instead:
//
//   curl -sS -L "https://hoop.dev/docs/<path>.md" | head -8 \
//     | grep -q "Runtime control for agents" && echo DEAD
export const docsUrl = {
  concepts: {
    agents: 'https://hoop.dev/docs/concepts/agents',
  },
  features: {
    runbooks: 'https://hoop.dev/docs/learn/features/runbooks',
    sessionRecording: 'https://hoop.dev/docs/learn/features/session-recording',
    // The Gateway's Live Data Masking, which is what /features/data-masking edits.
    // NOT https://hoop.dev/docs/features/data-masking — that page documents the
    // Sidecar `mask` block, a different engine with a different vocabulary.
    aiDatamasking: 'https://hoop.dev/docs/learn/features/live-data-masking',
    aiSessionAnalyzer: 'https://hoop.dev/docs/learn/features/ai-session-analyzer',
    abac: 'https://hoop.dev/docs/learn/features/abac',
    accessControl: 'https://hoop.dev/docs/learn/features/access-control',
    // Not in config.cljs — the CLJS access request page linked to :reviews
    // for want of a better key. Don't delete this as mirror drift.
    accessRequests: 'https://hoop.dev/docs/learn/features/access-requests/action',
    jitAccessRequests: 'https://hoop.dev/docs/learn/features/access-requests/jit',
    guardrails: 'https://hoop.dev/docs/learn/features/guardrails',
  },
  introduction: {
    gettingStarted: 'https://hoop.dev/docs/introduction/getting-started',
  },
  quickstart: {
    databases: 'https://hoop.dev/docs/quickstart/databases',
    cloudServices: 'https://hoop.dev/docs/quickstart/cloud-services',
    webApplications: 'https://hoop.dev/docs/quickstart/web-applications',
    developmentEnvironments: 'https://hoop.dev/docs/quickstart/development-environments',
    ssh: 'https://hoop.dev/docs/quickstart/ssh',
  },
  setup: {
    architecture: 'https://hoop.dev/docs/setup/architecture',
    deployment: {
      overview: 'https://hoop.dev/docs/setup/deployment',
      kubernetes: 'https://hoop.dev/docs/setup/deployment/kubernetes',
      docker: 'https://hoop.dev/docs/setup/deployment/docker-compose',
      aws: 'https://hoop.dev/docs/setup/deployment/AWS',
    },
    configuration: {
      overview: 'https://hoop.dev/docs/setup/configuration',
      environmentVariables: 'https://hoop.dev/docs/setup/configuration/env-vars',
      reverseProxy: 'https://hoop.dev/docs/setup/configuration/reverse-proxy',
      identityProviders: 'https://hoop.dev/docs/setup/configuration/idp/get-started',
      secretsManager: 'https://hoop.dev/docs/setup/configuration/secrets-manager-configuration',
      liveDataMasking: 'https://hoop.dev/docs/setup/configuration/live-data-masking/get-started',
      rdsIamAuth: 'https://hoop.dev/docs/setup/configuration/rds-iam-auth',
    },
    apis: {
      apiKeys: 'https://hoop.dev/docs/setup/apis/api-key#api-key',
      overview: 'https://hoop.dev/docs/setup/apis',
    },
    licenseManagement: 'https://hoop.dev/docs/setup/license-management',
  },
  clients: {
    webApp: {
      overview: 'https://hoop.dev/docs/clients/webapp/overview',
      creatingResourceRoles: 'https://hoop.dev/docs/clients/webapp/creating-resource-roles',
      managingAccess: 'https://hoop.dev/docs/clients/webapp/managing-access',
      userManagement: 'https://hoop.dev/docs/clients/webapp/managing-access',
      monitoringSessions: 'https://hoop.dev/docs/clients/webapp/monitoring-sessions',
    },
    commandLine: {
      overview: 'https://hoop.dev/docs/clients/cli',
      windows: 'https://hoop.dev/docs/clients/cli#windows',
      macos: 'https://hoop.dev/docs/clients/cli#mac-os',
      linux: 'https://hoop.dev/docs/clients/cli#linux',
      managingConfiguration: 'https://hoop.dev/docs/clients/cli#managing-configuration',
    },
  },
  integrations: {
    slack: 'https://hoop.dev/docs/integrations/slack',
    teams: 'https://hoop.dev/docs/integrations/teams',
    jira: 'https://hoop.dev/docs/integrations/jira',
    svix: 'https://hoop.dev/docs/integrations/svix',
    awsConnect: 'https://hoop.dev/docs/integrations/aws',
  },
}
