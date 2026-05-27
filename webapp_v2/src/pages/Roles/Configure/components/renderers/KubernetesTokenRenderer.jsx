import { Stack, Title } from '@mantine/core'
import PredefinedFields from './shared/PredefinedFields'
import AllowInsecureSslSection from './shared/AllowInsecureSslSection'
import AgentSelectorSection from './shared/AgentSelectorSection'

// Kubernetes connection authenticated via a bearer token, no kubeconfig
// required. Mirrors CLJS server.cljs::kubernetes-token. The auth-token
// envvar carries a "Bearer " prefix that the form hides from the input
// and re-prefixes on save — added in the next pass; today this just
// uses the field schema directly.
const KUBERNETES_TOKEN_FIELDS = [
  {
    key: 'cluster_url',
    label: 'Cluster URL',
    required: true,
    placeholder: 'e.g. https://kubernetes.default.svc.cluster.local:443',
  },
  {
    key: 'authorization',
    label: 'Authorization token',
    required: true,
    placeholder: 'e.g. jwt.token.example',
  },
]

export default function KubernetesTokenRenderer({
  connection,
  availableSources,
  forceNewState,
}) {
  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>Kubernetes token</Title>
        <PredefinedFields
          connection={connection}
          fields={KUBERNETES_TOKEN_FIELDS}
          availableSources={availableSources}
          forceNewState={forceNewState}
        />
      </Stack>
      <AllowInsecureSslSection connection={connection} />
      <AgentSelectorSection />
    </Stack>
  )
}
