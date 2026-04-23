import { Stack, Text, Anchor, Table, Title, Grid, Box, Flex, ActionIcon } from '@mantine/core';
import { CopyButton } from '@mantine/core';
import { Copy } from 'lucide-react';
import CodeSnippet from '@/components/CodeSnippet';
import DocsBtnCallOut from '@/components/DocsBtnCallOut';
import { docsUrl } from '@/utils/docsUrl';

const HELM_DOCS_URL = 'https://helm.sh/docs/intro/install/';

function InlineCopy({ value }) {
  return (
    <CopyButton value={value}>
      {({ copy }) =>
        <ActionIcon variant="subtle" color="gray" size="xs" onClick={copy}>
          <Copy size={12} />
        </ActionIcon>}
    </CopyButton>
  );
}

export function DockerDeployment({ hoopKey }) {
  const dockerImage = 'hoophq/hoopdev:latest';
  const runCommand = `docker container run \\\n-e HOOP_KEY='${hoopKey}' \\\n--rm -d hoophq/hoopdev`;

  return (
    <Stack gap="lg">
      <Stack gap="xs">
        <Text size="sm" fw={700}>
          Docker image repository
        </Text>
        <Box p="md" style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 12 }}>
          <Flex gap="xs" align="center">
            <img src="/images/docker-blue.svg" alt="Docker" height={24} />
            <Text size="xs" style={{ flex: 1 }}>
              {dockerImage}
            </Text>
            <InlineCopy value={dockerImage} />
          </Flex>
        </Box>
      </Stack>

      <Stack gap="xs">
        <Text size="sm" fw={700}>
          Environment variables
        </Text>
        <Table verticalSpacing="xs" horizontalSpacing="sm" withTableBorder>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>env-var</Table.Th>
              <Table.Th>value</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            <Table.Tr>
              <Table.Td>
                <Flex gap="xs" align="center">
                  <Text size="sm" ff="monospace">
                    HOOP_KEY
                  </Text>
                  <InlineCopy value="HOOP_KEY" />
                </Flex>
              </Table.Td>
              <Table.Td>
                <Flex gap="xs" align="center">
                  <Text size="xs" ff="monospace" style={{ wordBreak: 'break-all' }}>
                    {hoopKey}
                  </Text>
                  <InlineCopy value={hoopKey} />
                </Flex>
              </Table.Td>
            </Table.Tr>
          </Table.Tbody>
        </Table>
      </Stack>

      <Stack gap="xs">
        <Text size="sm" fw={700}>
          Manually running in a Docker container
        </Text>
        <Text size="xs" c="dimmed">
          If preferred, it is also possible to configure it manually with the following command.
        </Text>
        <CodeSnippet code={runCommand} />
      </Stack>
    </Stack>
  );
}

export function KubernetesDeployment({ hoopKey }) {
  const valuesYml = `config:\n  HOOP_KEY: ${hoopKey}\nimage:\n  repository: hoophq/hoopdev\n  tag: latest`;
  const setVersion = `VERSION=$(curl -s https://releases.hoop.dev/release/latest.txt)\n `;
  const helmInstall = `helm upgrade --install hoopagent \\\noci://ghcr.io/hoophq/helm-charts/hoopagent-chart --version $VERSION \\\n--set config.HOOP_KEY=${hoopKey} `;
  const helmManifests = `helm template hoopagent \\\noci://ghcr.io/hoophq/helm-charts/hoopagent-chart --version $VERSION \\\n--set 'config.HOOP_KEY=${hoopKey}' \\\n--set 'extraSecret=AWS_REGION=us-east-1' \\`;
  const deploymentYml = `apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: hoopagent\nspec:\n  replicas: 1\n  selector:\n    matchLabels:\n      app: hoopagent\n  template:\n    metadata:\n      labels:\n        app: hoopagent\n    spec:\n      containers:\n      - name: hoopagent\n        image: hoophq/hoopdev\n        env:\n        - name: HOOP_KEY\n          value: '${hoopKey}'`;

  return (
    <Stack gap="lg">
      <Stack gap="xs">
        <Text size="sm" fw={700}>
          Minimal configuration
        </Text>
        <Text size="xs" c="dimmed">
          Include the following parameters for standard installation, for a full configuration.{' '}
          <Anchor href={`${docsUrl.setup.deployment.kubernetes}#agent-deployment`} target="_blank" size="xs">
            Check your docs.
          </Anchor>
        </Text>
        <Text size="sm" fw={700}>
          values.yml
        </Text>
        <CodeSnippet code={valuesYml} />
      </Stack>

      <Stack gap="xs">
        <Text size="sm" fw={700}>
          Standalone deployment
        </Text>
        <Text size="sm" fw={700}>
          Helm
        </Text>
        <Text size="xs" c="dimmed">
          Make sure you have Helm installed.{' '}
          <Anchor href={HELM_DOCS_URL} target="_blank" size="xs">
            Check the Helm installation guide
          </Anchor>
        </Text>
        <CodeSnippet code={setVersion} />
        <CodeSnippet code={helmInstall} />
        <Text size="xs" c="dimmed">
          Using helm manifests
        </Text>
        <CodeSnippet code={helmManifests} />
      </Stack>

      <Stack gap="xs">
        <Text size="sm" fw={700}>
          deployment.yml
        </Text>
        <Text size="xs" c="dimmed">
          For more kubernetes configuration.{' '}
          <Anchor href={`${docsUrl.setup.deployment.kubernetes}#sidecar-container`} target="_blank" size="xs">
            Check the Hoop docs
          </Anchor>
        </Text>
        <CodeSnippet code={deploymentYml} />
      </Stack>
    </Stack>
  );
}

export function LocalDeployment({ hoopKey }) {
  const exportCmd = `export HOOP_KEY=${hoopKey}`;
  const startCmd = `hoop agent start`;

  return (
    <Stack gap="lg">
      <Stack gap="xs">
        <Text size="sm" fw={700}>
          Install Hoop CLI
        </Text>
        <DocsBtnCallOut text="See our installation docs for your OS" href={docsUrl.clients.commandLine.overview} />
      </Stack>

      <Stack gap="xs">
        <Text size="sm" fw={700}>
          Export your HOOP_KEY and run it
        </Text>
        <Text size="xs" c="dimmed">
          Run the following command to export your HOOP_KEY and start the agent.
        </Text>
        <CodeSnippet code={exportCmd} />
        <CodeSnippet code={startCmd} />
      </Stack>

      <Stack gap="xs">
        <Text size="sm" fw={700}>
          The hoop agent CLI
        </Text>
        <Text size="xs" c="dimmed">
          Learn how to operate this agent using its CLI
        </Text>
        <DocsBtnCallOut text="Check our docs" href={`${docsUrl.concepts.agents}#standard-mode`} />
      </Stack>
    </Stack>
  );
}

export function DeploymentMain({ installMethod, hoopKey }) {
  return (
    <Grid gutter="xl">
      <Grid.Col span={3}>
        <Stack gap="sm">
          <Stack gap="xs">
            <Title order={4}>Agent deployment</Title>
            <Text size="sm" c="dimmed">
              Setup your Agent in your infrastructure.
            </Text>
          </Stack>
          <DocsBtnCallOut text="Learn more about Agents" href={docsUrl.setup.agents} />
        </Stack>
      </Grid.Col>
      <Grid.Col span={9}>
        {installMethod === 'docker' && <DockerDeployment hoopKey={hoopKey} />}
        {installMethod === 'kubernetes' && <KubernetesDeployment hoopKey={hoopKey} />}
        {installMethod === 'local' && <LocalDeployment hoopKey={hoopKey} />}
      </Grid.Col>
    </Grid>
  );
}
