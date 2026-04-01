import { Stack, Text, Anchor, Table, Title } from '@mantine/core'
import CodeSnippet from '@/components/CodeSnippet'

const HELM_DOCS_URL = 'https://hoop.dev/docs/setup/agents#kubernetes'
const HOOP_DOCS_URL = 'https://hoop.dev/docs/setup/agents'

export function DockerDeployment({ hoopKey }) {
  const dockerImage = 'hoophq/hoopdev:latest'
  const runCommand = `docker run -e HOOP_KEY="${hoopKey}" ${dockerImage}`

  return (
    <Stack gap="md">
      <Stack gap={4}>
        <Text size="sm" fw={500}>Docker image</Text>
        <CodeSnippet code={dockerImage} />
      </Stack>

      <Stack gap={4}>
        <Text size="sm" fw={500}>Environment variables</Text>
        <Table withTableBorder withColumnBorders>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>env-var</Table.Th>
              <Table.Th>value</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            <Table.Tr>
              <Table.Td>
                <Text size="sm" ff="monospace">HOOP_KEY</Text>
              </Table.Td>
              <Table.Td>
                <CodeSnippet code={hoopKey} />
              </Table.Td>
            </Table.Tr>
          </Table.Tbody>
        </Table>
      </Stack>

      <Stack gap={4}>
        <Text size="sm" fw={500}>Run command</Text>
        <CodeSnippet code={runCommand} />
      </Stack>
    </Stack>
  )
}

export function KubernetesDeployment({ hoopKey }) {
  const valuesYml = `config:
  HOOP_KEY: "${hoopKey}"`

  const helmInstall = `helm repo add hoophq https://hoophq.github.io/helm-charts
helm install hoop-agent hoophq/hoop-agent -f values.yml`

  const k8sManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: hoop-agent
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hoop-agent
  template:
    metadata:
      labels:
        app: hoop-agent
    spec:
      containers:
        - name: hoop-agent
          image: hoophq/hoopdev:latest
          env:
            - name: HOOP_KEY
              value: "${hoopKey}"`

  return (
    <Stack gap="md">
      <Stack gap={4}>
        <Text size="sm" fw={500}>Minimal configuration — values.yml</Text>
        <CodeSnippet code={valuesYml} />
      </Stack>

      <Stack gap={4}>
        <Text size="sm" fw={500}>
          Helm installation{' '}
          <Anchor href={HELM_DOCS_URL} target="_blank" size="sm">docs</Anchor>
        </Text>
        <CodeSnippet code={helmInstall} />
      </Stack>

      <Stack gap={4}>
        <Text size="sm" fw={500}>Kubernetes manifest — deployment.yml</Text>
        <CodeSnippet code={k8sManifest} />
      </Stack>
    </Stack>
  )
}

export function LocalDeployment({ hoopKey }) {
  const exportCmd = `export HOOP_KEY="${hoopKey}"`
  const startCmd = `hoop agent start`

  return (
    <Stack gap="md">
      <Stack gap={4}>
        <Text size="sm" fw={500}>
          Install Hoop CLI{' '}
          <Anchor href={HOOP_DOCS_URL} target="_blank" size="sm">docs</Anchor>
        </Text>
      </Stack>

      <Stack gap={4}>
        <Text size="sm" fw={500}>Export your agent key</Text>
        <CodeSnippet code={exportCmd} />
      </Stack>

      <Stack gap={4}>
        <Text size="sm" fw={500}>Start the agent</Text>
        <CodeSnippet code={startCmd} />
      </Stack>
    </Stack>
  )
}
