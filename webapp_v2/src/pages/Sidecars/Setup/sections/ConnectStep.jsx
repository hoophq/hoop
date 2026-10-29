import { useState } from 'react'
import { Anchor, Grid, Group, SimpleGrid, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { Boxes, Container, ExternalLink, SquareTerminal } from 'lucide-react'
import { Link } from 'react-router-dom'
import Button from '@/components/Button'
import CodeSnippet from '@/components/CodeSnippet'
import SelectionCard from '@/components/SelectionCard'
import Tabs from '@/components/Tabs'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'

function NumberedBlock({ n, title, children }) {
  return (
    <Group align="flex-start" gap="md" wrap="nowrap">
      {/* A bordered icon is a pill, like ActionIcon: 100px caps at a circle. */}
      <ThemeIcon size={32} radius="100px" variant="default">
        <Text size="sm" fw={700}>
          {n}
        </Text>
      </ThemeIcon>
      <Stack gap="sm" flex={1} miw={0}>
        {/* As tall as the circle, so the title sits on its center line. */}
        <Group mih={32}>
          <Text fw={600}>{title}</Text>
        </Group>
        {children}
      </Stack>
    </Group>
  )
}

/**
 * A block whose command has more than one spelling. The tabs are the sources
 * the documentation lists for that one setting, in its precedence order, so a
 * reader who knows the docs finds the same choices here.
 */
function SourceTabs({ sources }) {
  const [tab, setTab] = useState(sources[0].value)
  return (
    <Tabs value={tab} onChange={setTab}>
      <Tabs.List>
        {sources.map((s) => (
          <Tabs.Tab key={s.value} value={s.value}>
            {s.label}
          </Tabs.Tab>
        ))}
      </Tabs.List>
      {sources.map((s) => (
        <Tabs.Panel key={s.value} value={s.value} pt="sm">
          <CodeSnippet code={s.code} variant={s.variant ?? 'black'} />
        </Tabs.Panel>
      ))}
    </Tabs>
  )
}

// The three places a sidecar runs, the tabs of the documentation's "Connect a
// Sidecar" page. Both flows ask: create for where to deploy it, connect for
// where it already runs.
const DEPLOY_TARGETS = [
  {
    value: 'k8s',
    icon: Boxes,
    title: 'Kubernetes',
    description: { create: 'Run it as a container in your cluster.', connect: 'It runs as a container in your cluster.' },
  },
  {
    value: 'compose',
    icon: Container,
    title: 'Docker Compose',
    description: {
      create: 'Run it as a service with Docker Compose.',
      connect: 'It runs as a service with Docker Compose.',
    },
  },
  {
    value: 'cli',
    icon: SquareTerminal,
    title: 'CLI / Binary',
    description: {
      create: 'Run it on a VM or your machine with the hoop CLI.',
      connect: 'It runs on a VM or your machine with the hoop CLI.',
    },
  },
]

// hoophq/hoopsidecar is the dedicated sidecar image: its own entrypoint runs
// the relay and reads the config at /etc/hoop-inspect/config.yaml, which is
// what the helm chart deploys (deploy/helm-chart/chart/sidecar). The relay also
// ships inside hoophq/hoopdev and hoophq/hoop as `hoop start sidecar`, which the
// CLI block uses. See sidecar/README.md.
//
// The container takes the Control Plane URL and the Sidecar token as
// environment variables: the daemon refuses to start with a control plane and
// no token (resolveConfigSource, sidecar/daemon/controlplane.go).
const composeConfig = (image, url, token) => `services:
  hoop-sidecar:
    image: ${image}
    environment:
      HOOP_CONTROL_PLANE_URL: ${url}
      HOOP_SIDECAR_TOKEN: ${token}
    volumes:
      - ./config.yaml:/etc/hoop-inspect/config.yaml:ro
    ports:
      - "19000:19000"    # admin only
    healthcheck:
      test: ["CMD-SHELL", "curl -sf http://127.0.0.1:19000/healthz || exit 1"]`

// The token goes in a Secret rather than the manifest: a Deployment is the
// thing most likely to sit in git.
const k8sSecret = (token) => `kubectl create secret generic hoop-sidecar-token \\
  --from-literal=token=${token}`

const k8sEnv = (image, url) => `# in your Deployment's container spec
image: ${image}
env:
  - name: HOOP_CONTROL_PLANE_URL
    value: ${url}
  - name: HOOP_SIDECAR_TOKEN
    valueFrom:
      secretKeyRef:
        name: hoop-sidecar-token
        key: token`

// A sidecar that already runs keeps its image and its config file; it only
// gains the two variables (install/kubernetes.mdx, "Control Plane").
const k8sConnectEnv = (url) => `# add to your Deployment's container spec
env:
  - name: HOOP_CONTROL_PLANE_URL
    value: ${url}
  - name: HOOP_SIDECAR_TOKEN
    valueFrom:
      secretKeyRef:
        name: hoop-sidecar-token
        key: token`

const composeConnectEnv = (url, token) => `services:
  hoop-sidecar:    # your existing service
    environment:
      HOOP_CONTROL_PLANE_URL: ${url}
      HOOP_SIDECAR_TOKEN: ${token}`

/**
 * Step 1 of the sidecar wizard. The control plane issues a token for a named
 * sidecar (POST /sidecars), so the name comes first. The reader then picks
 * where the sidecar runs, and the blocks that follow are the documented steps
 * for that one target: create installs it first, connect only points it at Hoop.
 */
export default function ConnectStep({ mode, sidecar, token, controlPlaneUrl, imageRef, creating, error, onCreate }) {
  const [name, setName] = useState('')
  const [target, setTarget] = useState('k8s')
  const created = !!sidecar
  const isConnect = mode === 'connect'

  const handleSubmit = (e) => {
    e.preventDefault()
    if (created || !name.trim()) return
    onCreate(name.trim())
  }

  // Shown once, in one place: the token is a bearer credential the control
  // plane never returns again. The deploy snippets below reference it, but this
  // is where the reader is told to copy it.
  const tokenBlock = {
    key: 'token',
    title: 'Copy the sidecar token',
    node: (
      <>
        <CodeSnippet code={token} />
        <Text size="xs" c="dimmed">
          <Text component="span" fw={700}>
            Shown once.
          </Text>
          {
            ' Copy it before you continue. Leaving this page discards it; delete the sidecar to get a new one.'
          }
        </Text>
      </>
    ),
  }

  // The create flow branches on the deploy target. Kubernetes keeps the full
  // manifest in the docs and shows only the two settings the sidecar needs;
  // Docker Compose and the CLI show the whole flow on screen.
  const targetBlocks = {
    k8s: [
      tokenBlock,
      {
        key: 'install',
        title: 'Create the token secret',
        node: (
          <>
            <CodeSnippet code={k8sSecret(token)} variant="black" />
            <Text size="xs" c="dimmed">
              The Deployment reads the Sidecar token from this Secret.
            </Text>
          </>
        ),
      },
      {
        key: 'config',
        title: 'Configure the container',
        node: (
          <>
            <CodeSnippet code={k8sEnv(imageRef, controlPlaneUrl)} />
            <Text size="xs" c="dimmed">
              {'The Control Plane URL and the Sidecar token are all it needs here. '}
              <Anchor href={docsUrl.sidecar.getStarted} target="_blank" rel="noopener noreferrer" size="xs">
                Running the Sidecar
              </Anchor>
              {' has the full Deployment and ConfigMap.'}
            </Text>
          </>
        ),
      },
    ],
    compose: [
      {
        key: 'install',
        title: 'Pull the image',
        node: (
          <>
            <CodeSnippet code={`docker pull ${imageRef}`} variant="black" />
            <Text size="xs" c="dimmed">
              The dedicated sidecar image. docker compose up pulls it too.
            </Text>
          </>
        ),
      },
      tokenBlock,
      {
        key: 'config',
        title: 'Configure the service',
        node: (
          <>
            <CodeSnippet code={composeConfig(imageRef, controlPlaneUrl, token)} />
            <Text size="xs" c="dimmed">
              {'Save this as compose.yaml next to your config.yaml. '}
              <Anchor href={docsUrl.sidecar.getStarted} target="_blank" rel="noopener noreferrer" size="xs">
                Running the Sidecar
              </Anchor>
              {' walks through the config file itself.'}
            </Text>
          </>
        ),
      },
      {
        key: 'run',
        title: 'Run it',
        node: <CodeSnippet code="docker compose up -d" variant="black" />,
      },
    ],
    cli: [
      {
        key: 'install',
        title: 'Install the hoop CLI',
        node: (
          <Text size="xs" c="dimmed">
            {'See the '}
            <Anchor href={docsUrl.clients.commandLine.overview} target="_blank" rel="noopener noreferrer" size="xs">
              installation docs for your OS
            </Anchor>
            {'.'}
          </Text>
        ),
      },
      tokenBlock,
      {
        key: 'config',
        title: 'Point the sidecar at Hoop',
        node: (
          <>
            <CodeSnippet code={`control_plane_url: ${controlPlaneUrl}`} />
            <Text size="xs" c="dimmed">
              config.yaml. The token is a flag, not a config file key.
            </Text>
          </>
        ),
      },
      {
        key: 'run',
        title: 'Start the sidecar with the token',
        node: (
          <>
            <CodeSnippet code={`hoop start sidecar --config config.yaml --token "${token}"`} variant="black" />
            <Text size="xs" c="dimmed">
              {
                'Run this on the host that reaches your resources. Listeners come from its local file; guardrails, masking and analyzer settings come from Hoop.'
              }
            </Text>
          </>
        ),
      },
    ],
  }

  // The secret step is the same in both flows: a running pod reads the token
  // from it just like a new one.
  const k8sSecretBlock = targetBlocks.k8s[1]
  const keepsFileNote = (
    <Text size="xs" c="dimmed">
      It keeps running its config file until you move the configuration to Hoop from the sidecar page.
    </Text>
  )

  // The connect flow is for a sidecar that already runs, so nothing is
  // installed: point it at Hoop, take the token, restart it where it runs.
  const connectBlocks = {
    k8s: [
      tokenBlock,
      k8sSecretBlock,
      {
        key: 'config',
        title: 'Point the container at Hoop',
        node: (
          <>
            <CodeSnippet code={k8sConnectEnv(controlPlaneUrl)} />
            <Text size="xs" c="dimmed">
              {'Keep its image and config file as they are. Installed with the Helm chart? Set '}
              <Anchor href={docsUrl.sidecar.kubernetesControlPlane} target="_blank" rel="noopener noreferrer" size="xs">
                controlPlane in your values
              </Anchor>
              {' instead.'}
            </Text>
          </>
        ),
      },
      {
        key: 'start',
        title: 'Restart it',
        node: (
          <>
            <CodeSnippet code="kubectl rollout restart deployment/<your-sidecar-deployment>" />
            {keepsFileNote}
          </>
        ),
      },
    ],
    compose: [
      tokenBlock,
      {
        key: 'config',
        title: 'Point the service at Hoop',
        node: (
          <>
            <CodeSnippet code={composeConnectEnv(controlPlaneUrl, token)} />
            <Text size="xs" c="dimmed">
              Add the two variables to the service in your compose.yaml. Keep its config file mount.
            </Text>
          </>
        ),
      },
      {
        key: 'start',
        title: 'Restart it',
        node: (
          <>
            <CodeSnippet code="docker compose up -d" />
            <Text size="xs" c="dimmed">
              Compose recreates the service with the new variables.
            </Text>
            {keepsFileNote}
          </>
        ),
      },
    ],
    cli: [
      {
        key: 'url',
        title: 'Point the sidecar at Hoop',
        node: (
          <>
            <SourceTabs
              sources={[
                { value: 'file', label: 'config.yaml', code: `control_plane_url: ${controlPlaneUrl}` },
                { value: 'env', label: 'Environment Variables', code: `export HOOP_CONTROL_PLANE_URL=${controlPlaneUrl}` },
              ]}
            />
            <Text size="xs" c="dimmed">
              The environment variable outranks the config file.
            </Text>
          </>
        ),
      },
      tokenBlock,
      {
        key: 'start',
        title: 'Restart your sidecar with the token',
        node: (
          <>
            <SourceTabs
              sources={[
                {
                  value: 'flag',
                  label: 'CLI Options',
                  code: `hoop start sidecar --config config.yaml --token "${token}"`,
                  variant: 'black',
                },
                {
                  value: 'env',
                  label: 'Environment Variables',
                  code: `export HOOP_SIDECAR_TOKEN=${token}\nhoop start sidecar --config config.yaml`,
                  variant: 'black',
                },
              ]}
            />
            <Text size="xs" c="dimmed">
              {
                'Run this on the host that reaches your resources. Listeners come from its local file; guardrails, masking and analyzer settings come from Hoop.'
              }
            </Text>
          </>
        ),
      },
    ],
  }

  const blocks = (isConnect ? connectBlocks : targetBlocks)[target]

  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Title order={4}>Connect it to Hoop</Title>
          <Text size="sm" c="dimmed">
            {isConnect
              ? 'Name the sidecar you already run to issue its token, pick where it runs, then work through each block in order.'
              : 'Name the sidecar you are about to deploy to issue its token, pick where it runs, then work through each block in order.'}
          </Text>
          <Button
            component="a"
            href={docsUrl.sidecar.connectToControlPlane}
            target="_blank"
            rel="noopener noreferrer"
            variant="outline"
            size="sm"
            rightSection={<ExternalLink size={14} aria-hidden="true" />}
            w="fit-content"
            mt="xs"
          >
            Connect a Sidecar
          </Button>
        </Stack>
      </Grid.Col>

      <Grid.Col span={5}>
        <Stack gap="xl">
          <form onSubmit={handleSubmit}>
            <Stack gap="md" align="flex-start">
              <TextInput
                label="Name"
                placeholder="payments-sidecar"
                value={name}
                onChange={(e) => setName(e.currentTarget.value)}
                error={error}
                required
                disabled={created}
                w="100%"
                maw={420}
              />
              {!created && (
                <Button type="submit" size="sm" loading={creating} disabled={!name.trim()}>
                  Create sidecar
                </Button>
              )}
              {error?.includes('already exists') && (
                <Text size="xs" c="dimmed">
                  {'A sidecar with this name exists. Its token was shown once; '}
                  <Anchor component={Link} to="/sidecars" size="xs">
                    delete it from the list
                  </Anchor>
                  {' and create it again, or pick another name.'}
                </Text>
              )}
            </Stack>
          </form>

          {created && (
            <Stack gap="xl">
              <Stack gap="sm">
                <Text fw={600}>
                  {isConnect ? 'Where does your sidecar run?' : 'Where do you want to deploy the sidecar?'}
                </Text>
                <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
                  {DEPLOY_TARGETS.map((t) => (
                    <SelectionCard
                      key={t.value}
                      icon={t.icon}
                      title={t.title}
                      description={t.description[isConnect ? 'connect' : 'create']}
                      selected={target === t.value}
                      onClick={() => setTarget(t.value)}
                    />
                  ))}
                </SimpleGrid>
              </Stack>

              {blocks.map((block, i) => (
                <NumberedBlock key={block.key} n={i + 1} title={block.title}>
                  {block.node}
                </NumberedBlock>
              ))}
            </Stack>
          )}
        </Stack>
      </Grid.Col>
    </Grid>
  )
}
