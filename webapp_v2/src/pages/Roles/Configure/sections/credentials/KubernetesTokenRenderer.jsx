import { Stack, Title } from '@mantine/core'
import SourcedInput from '@/components/SourcedInput'
import { sourceOptionsFor } from '../../components/SecretField/util'
import {
  decodeForDisplay,
  encodeSecretForSource,
  sourceFromEncodedValue,
  SOURCES,
} from '../../utils/secretsCodec'
import { useConfigureRoleStore } from '../../store'
import AllowInsecureSslSection from './shared/AllowInsecureSslSection'
import AgentSelectorSection from './shared/AgentSelectorSection'

const CLUSTER_URL_KEY = 'envvar:REMOTE_URL'
const AUTH_TOKEN_KEY = 'envvar:HEADER_AUTHORIZATION'
const BEARER_PREFIX = 'Bearer '

// Kubernetes connection authenticated via a bearer token, no kubeconfig
// required. Mirrors CLJS webapp/.../setup/server.cljs::kubernetes-token.
//
// Two quirks vs the default predefined-field layout:
//
//  - Wire keys are REMOTE_URL and HEADER_AUTHORIZATION (not the
//    catalog's CLUSTER_URL / AUTHORIZATION) — that's how the agent
//    talks to the Kubernetes API server. We translate the user-facing
//    labels here.
//
//  - The authorization value carries a `Bearer ` prefix the Kubernetes
//    API server requires. CLJS strips it from the input for display
//    and re-prefixes on save when the source is manual. Vault / AWS
//    references stay verbatim — those resolve to the raw token at
//    runtime and the agent handles the prefix downstream.
//
// Both fields round-trip plaintext (the gateway returns their values —
// see shouldRoundTripSecrets), so we always render an editable
// SourcedInput; no Set/Replace mode here.
export default function KubernetesTokenRenderer({
  connection,
  availableSources,
  forceNewState,
}) {
  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const fieldSources = useConfigureRoleStore((s) => s.fieldSources)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const setFieldSource = useConfigureRoleStore((s) => s.setFieldSource)

  const currentSecrets = connection.secret || {}
  const defaultSource = availableSources?.[0] || SOURCES.MANUAL

  // forceNewState is set when the user has just switched the connection
  // method (Manual ↔ Secrets Manager). Old plaintext values don't apply
  // to the new method, so we render the fields blank and let the user
  // re-enter.
  const fieldValue = (key, { transform } = {}) => {
    const staged = stagedSecrets[key]
    const encoded = staged
      ? staged.value
      : forceNewState
        ? undefined
        : currentSecrets[key]
    const plain = decodeForDisplay(encoded)
    return transform ? transform(plain) : plain
  }

  const fieldSource = (key) => {
    const staged = stagedSecrets[key]
    const encoded = staged
      ? staged.value
      : forceNewState
        ? ''
        : currentSecrets[key] || ''
    return (
      fieldSources[key] ||
      (encoded ? sourceFromEncodedValue(encoded) : null) ||
      defaultSource
    )
  }

  const stripBearer = (s) =>
    s && s.startsWith(BEARER_PREFIX) ? s.slice(BEARER_PREFIX.length) : s

  // Manual-source tokens get the Bearer prefix added back on save.
  // Provider references (vault / aws) pass through unchanged — the
  // agent resolves and prefixes them at runtime.
  const encodeWithBearer = (plain, source) => {
    if (source !== SOURCES.MANUAL || !plain) {
      return encodeSecretForSource(plain, source)
    }
    const prefixed = plain.startsWith(BEARER_PREFIX) ? plain : BEARER_PREFIX + plain
    return encodeSecretForSource(prefixed, source)
  }

  const clusterUrlSource = fieldSource(CLUSTER_URL_KEY)
  const clusterUrl = fieldValue(CLUSTER_URL_KEY)

  const authTokenSource = fieldSource(AUTH_TOKEN_KEY)
  const authToken = fieldValue(AUTH_TOKEN_KEY, { transform: stripBearer })

  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>Kubernetes token</Title>
        <Stack gap="lg">
          <SourcedInput
            label="Cluster URL"
            required
            placeholder="e.g. https://kubernetes.default.svc.cluster.local:443"
            value={clusterUrl}
            onChange={(plain) =>
              replaceSecret(
                CLUSTER_URL_KEY,
                encodeSecretForSource(plain, clusterUrlSource),
              )
            }
            source={clusterUrlSource}
            sources={sourceOptionsFor(availableSources)}
            onSourceChange={(nextSource) => {
              replaceSecret(
                CLUSTER_URL_KEY,
                encodeSecretForSource(clusterUrl, nextSource),
              )
              setFieldSource(CLUSTER_URL_KEY, nextSource)
            }}
          />
          <SourcedInput
            label="Authorization token"
            required
            placeholder="e.g. jwt.token.example"
            value={authToken}
            onChange={(plain) =>
              replaceSecret(AUTH_TOKEN_KEY, encodeWithBearer(plain, authTokenSource))
            }
            source={authTokenSource}
            sources={sourceOptionsFor(availableSources)}
            onSourceChange={(nextSource) => {
              replaceSecret(AUTH_TOKEN_KEY, encodeWithBearer(authToken, nextSource))
              setFieldSource(AUTH_TOKEN_KEY, nextSource)
            }}
          />
        </Stack>
      </Stack>
      <AllowInsecureSslSection connection={connection} />
      <AgentSelectorSection />
    </Stack>
  )
}
