// Single source of truth for documentation URLs — mirrors webapp/src/webapp/config.cljs docs-url
export const docsUrl = {
  concepts: {
    agents: 'https://hoop.dev/docs/concepts/agents',
    connections: 'https://hoop.dev/docs/concepts/connections',
  },
  features: {
    runbooks: 'https://hoop.dev/docs/learn/features/runbooks',
    sessionRecording: 'https://hoop.dev/docs/learn/features/session-recording',
    aiDatamasking: 'https://hoop.dev/docs/learn/features/ai-data-masking',
    aiSessionAnalyzer: 'https://hoop.dev/docs/learn/features/ai-session-analyzer',
    attributes: 'https://hoop.dev/docs/learn/features/attributes',
    accessControl: 'https://hoop.dev/docs/learn/features/access-control',
    reviews: 'https://hoop.dev/docs/learn/features/reviews/overview',
    jitReviews: 'https://hoop.dev/docs/learn/features/reviews/jit-reviews',
    commandReviews: 'https://hoop.dev/docs/learn/features/reviews/command-reviews',
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
    agents: 'https://hoop.dev/docs/setup/agents',
    architecture: 'https://hoop.dev/docs/setup/architecture',
    deployment: {
      overview: 'https://hoop.dev/docs/setup/deployment',
      kubernetes: 'https://hoop.dev/docs/setup/deployment/kubernetes',
      docker: 'https://hoop.dev/docs/setup/deployment/docker-compose',
      aws: 'https://hoop.dev/docs/setup/deployment/AWS',
      onPremises: 'https://hoop.dev/docs/setup/deployment/on-premises',
    },
    configuration: {
      overview: 'https://hoop.dev/docs/setup/configuration',
      environmentVariables: 'https://hoop.dev/docs/setup/configuration/environment-variables',
      reverseProxy: 'https://hoop.dev/docs/setup/configuration/reverse-proxy',
      identityProviders: 'https://hoop.dev/docs/setup/configuration/idp/get-started',
      secretsManager: 'https://hoop.dev/docs/setup/configuration/secrets-manager-configuration',
      aiDataMasking: 'https://hoop.dev/docs/setup/configuration/ai-data-masking',
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
      creatingConnection: 'https://hoop.dev/docs/clients/webapp/creating-connection',
      managingAccess: 'https://hoop.dev/docs/clients/webapp/managing-accesss',
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
