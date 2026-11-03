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
 * reader who knows the docs finds the same choices here. A source is one
 * `code`, or a `node` when its spelling takes more than one block.
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
          {s.node ?? <CodeSnippet code={s.code} variant={s.variant ?? 'black'} />}
        </Tabs.Panel>
      ))}
    </Tabs>
  )
}

function Note({ children }) {
  return (
    <Text size="xs" c="dimmed">
      {children}
    </Text>
  )
}

function DocLink({ href, children }) {
  return (
    <Anchor href={href} target="_blank" rel="noopener noreferrer" size="xs">
      {children}
    </Anchor>
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
    value: 'docker',
    icon: Container,
    title: 'Docker',
    description: { create: 'Run it as a container with Docker.', connect: 'It runs as a container with Docker.' },
  },
  {
    value: 'cli',
    icon: SquareTerminal,
    title: 'CLI / Bare metal',
    description: {
      create: 'Run it on a server, a VM or your machine with the hoop CLI.',
      connect: 'It runs on a server, a VM or your machine with the hoop CLI.',
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
//
// Every config file below names a listener: the Control Plane imports the file
// on the first start, and that import fails with none (control-plane/
// connect-sidecar.mdx). A container binds admin on 0.0.0.0, because a
// published port cannot reach the container's 127.0.0.1.
const LISTENER_EXAMPLE = (host) => `listeners:
  - name: appdb
    protocol: postgres
    listen: ${host}:15432
    upstream: <your-database-host>:5432`

const dockerConfig = `admin:
  listen: 0.0.0.0:19000      # the admin API port
${LISTENER_EXAMPLE('0.0.0.0')}`

const cliConfig = (url) => `control_plane_url: ${url}
admin:
  listen: 127.0.0.1:19000    # the admin API port
${LISTENER_EXAMPLE('127.0.0.1')}`

const dockerRun = (image, url, token) => `docker run -d --name hoop-sidecar \\
  -e HOOP_CONTROL_PLANE_URL=${url} \\
  -e HOOP_SIDECAR_TOKEN=${token} \\
  -v "$PWD/config.yaml:/etc/hoop-inspect/config.yaml:ro" \\
  -p 15432:15432 -p 19000:19000 \\
  ${image}`

// A container keeps the variables it started with, so connect creates it
// again. Only the two flags are new; the rest of the command is the reader's
// own, so the snippet holds no placeholder a shell would misread.
const dockerConnectFlags = (url, token) => `-e HOOP_CONTROL_PLANE_URL=${url} \\
-e HOOP_SIDECAR_TOKEN=${token} \\`

// The chart's `config` is the sidecar's config file, indented under one key.
// The chart's own Service publishes only the admin port: each listener needs a
// laneServices entry, or nothing in the cluster reaches it.
const helmValues = (url, token) => `controlPlane:
  url: ${url}
  token: ${token}
config:
  admin:
    listen: 0.0.0.0:19000      # the admin API port; the chart needs 19000
${LISTENER_EXAMPLE('0.0.0.0')
  .split('\n')
  .map((line) => `  ${line}`)
  .join('\n')}
laneServices:
  appdb:                       # a Service for the appdb listener
    enabled: true
    ports:
      - port: 15432`

const helmConnectValues = (url, token) => `controlPlane:
  url: ${url}
  token: ${token}`

const HELM_INSTALL = `VERSION=$(curl -s https://releases.hoop.dev/release/latest.txt)
helm upgrade --install hoop-sidecar \\
  oci://ghcr.io/hoophq/helm-charts/hoopsidecar-chart --version $VERSION \\
  --namespace hoop --create-namespace \\
  -f values.yaml`

// The token goes in a Secret rather than the manifest: a Deployment is the
// thing most likely to sit in git.
const k8sSecret = (token) => `kubectl create secret generic hoop-sidecar-token \\
  --from-literal=token=${token}`

const K8S_ENV = (url) => `env:
  - name: HOOP_CONTROL_PLANE_URL
    value: ${url}
  - name: HOOP_SIDECAR_TOKEN
    valueFrom:
      secretKeyRef:
        name: hoop-sidecar-token
        key: token`

const k8sEnv = (image, url) => `# in your Deployment's container spec
image: ${image}
${K8S_ENV(url)}`

// A sidecar that already runs keeps its image and its config file; it only
// gains the two variables (install/kubernetes.mdx, "Control Plane").
const k8sConnectEnv = (url) => `# add to your Deployment's container spec
${K8S_ENV(url)}`

const HOLDS_TOKEN = 'This file holds the token. Keep it out of a repository.'
const RUN_NOTE = 'Run this on the host that reaches your resources.'

/**
 * Step 1 of the sidecar wizard. The control plane issues a token for a named
 * sidecar (POST /sidecars), so the name comes first. The reader then picks
 * where the sidecar runs, and the blocks that follow are the documented steps
 * for that one target: create installs it first, connect only points it at Hoop.
 * The steps follow control-plane/connect-sidecar.mdx; keep the two together.
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
        <Note>
          <Text component="span" fw={700}>
            Shown once.
          </Text>
          {' Copy it before you continue. Leaving this page discards it; delete the sidecar to get a new one.'}
        </Note>
      </>
    ),
  }

  const configFileLink = <DocLink href={docsUrl.sidecar.configFile}>Config File Reference</DocLink>

  // The create flow branches on the deploy target. Kubernetes keeps the full
  // manifest in the docs; Docker and the CLI show the whole flow on screen.
  const targetBlocks = {
    k8s: [
      tokenBlock,
      {
        key: 'deploy',
        title: 'Deploy it',
        node: (
          <>
            <Note>
              The Helm chart does most of the work for you. Your own YAML gives you full control, and you write each
              object yourself. New to Kubernetes? Use the Helm chart.
            </Note>
            <SourceTabs
              sources={[
                {
                  value: 'helm',
                  label: 'Helm chart',
                  node: (
                    <Stack gap="sm">
                      <Note>Write a values.yaml:</Note>
                      <CodeSnippet code={helmValues(controlPlaneUrl, token)} />
                      <Note>
                        {`config is the sidecar config file. The Control Plane copies it once, on the first start. It needs at least one entry in listeners. Each listener needs a laneServices entry with its port. ${HOLDS_TOKEN}`}
                      </Note>
                      <Note>Install the chart:</Note>
                      <CodeSnippet code={HELM_INSTALL} />
                    </Stack>
                  ),
                },
                {
                  value: 'yaml',
                  label: 'Your own YAML',
                  node: (
                    <Stack gap="sm">
                      <Note>Put the token in a Secret:</Note>
                      <CodeSnippet code={k8sSecret(token)} />
                      <Note>Set the variables on the sidecar container. It takes no arguments:</Note>
                      <CodeSnippet code={k8sEnv(imageRef, controlPlaneUrl)} />
                      <Note>
                        {
                          'The container also needs the config file, from a ConfigMap mounted at /etc/hoop-inspect/config.yaml: at least one entry in listeners, and admin.listen on 0.0.0.0. '
                        }
                        <DocLink href={docsUrl.sidecar.kubernetesManifest}>Running as Sidecar</DocLink>
                        {' has the full Deployment and ConfigMap.'}
                      </Note>
                    </Stack>
                  ),
                },
              ]}
            />
          </>
        ),
      },
    ],
    docker: [
      tokenBlock,
      {
        key: 'config',
        title: 'Write the config file',
        node: (
          <>
            <Note>
              Create config.yaml in the folder where you run docker run. The Control Plane copies it once, on the
              first start.
            </Note>
            <CodeSnippet code={dockerConfig} />
            <Note>
              {
                'listeners names the database the sidecar sits in front of; the file needs at least one. admin.listen sets the port of the admin API. In a container, use 0.0.0.0: a published port cannot reach the container 127.0.0.1. The '
              }
              {configFileLink}
              {' has every key.'}
            </Note>
          </>
        ),
      },
      {
        key: 'run',
        title: 'Run it',
        node: (
          <>
            <CodeSnippet code={dockerRun(imageRef, controlPlaneUrl, token)} />
            <Note>
              <DocLink href={docsUrl.sidecar.containerImages}>Container Images</DocLink>
              {' has the image flavours.'}
            </Note>
          </>
        ),
      },
    ],
    cli: [
      {
        key: 'install',
        title: 'Install the hoop CLI',
        node: (
          <Note>
            {'See the '}
            <DocLink href={docsUrl.clients.commandLine.overview}>installation docs for your OS</DocLink>
            {'.'}
          </Note>
        ),
      },
      tokenBlock,
      {
        key: 'config',
        title: 'Write the config file',
        node: (
          <>
            <CodeSnippet code={cliConfig(controlPlaneUrl)} />
            <Note>
              {
                'The Control Plane copies this file once, on the first start. It needs at least one entry in listeners. The token is a flag, not a config file key. The '
              }
              {configFileLink}
              {' has every key.'}
            </Note>
          </>
        ),
      },
      {
        key: 'run',
        title: 'Start the sidecar with the token',
        node: (
          <>
            <CodeSnippet code={`hoop start sidecar --config config.yaml --token "${token}"`} />
            <Note>{RUN_NOTE}</Note>
          </>
        ),
      },
    ],
  }

  // The connect flow is for a sidecar that already runs, so nothing is
  // installed: point it at Hoop, take the token, restart it where it runs.
  const connectBlocks = {
    k8s: [
      tokenBlock,
      {
        key: 'start',
        title: 'Point it at Hoop and restart it',
        node: (
          <SourceTabs
            sources={[
              {
                value: 'helm',
                label: 'Helm chart',
                node: (
                  <Stack gap="sm">
                    <Note>Add the controlPlane block to its values:</Note>
                    <CodeSnippet code={helmConnectValues(controlPlaneUrl, token)} />
                    <Note>
                      {`Then run the helm upgrade you deployed it with. The chart restarts the pods. ${HOLDS_TOKEN} `}
                      <DocLink href={docsUrl.sidecar.kubernetesControlPlane}>Kubernetes</DocLink>
                      {' has every value.'}
                    </Note>
                  </Stack>
                ),
              },
              {
                value: 'yaml',
                label: 'Your own YAML',
                node: (
                  <Stack gap="sm">
                    <Note>Put the token in a Secret:</Note>
                    <CodeSnippet code={k8sSecret(token)} />
                    <Note>Add the variables to the sidecar container. Keep its image and config file as they are:</Note>
                    <CodeSnippet code={k8sConnectEnv(controlPlaneUrl)} />
                    <Note>Restart it to pick them up:</Note>
                    <CodeSnippet code="kubectl rollout restart deployment/<your-sidecar-deployment>" />
                  </Stack>
                ),
              },
            ]}
          />
        ),
      },
    ],
    docker: [
      tokenBlock,
      {
        key: 'start',
        title: 'Point it at Hoop and restart it',
        node: (
          <>
            <Note>Add these two flags to the docker run you started it with, before the image name:</Note>
            <CodeSnippet code={dockerConnectFlags(controlPlaneUrl, token)} />
            <Note>
              A container keeps the variables it started with, so create it again: remove it with docker rm -f, then
              run that command. Keep its image, mounts and ports as they are.
            </Note>
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
            <Note>The environment variable outranks the config file.</Note>
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
                },
                {
                  value: 'env',
                  label: 'Environment Variables',
                  code: `export HOOP_SIDECAR_TOKEN=${token}\nhoop start sidecar --config config.yaml`,
                },
              ]}
            />
            <Note>{`Stop it, then start it again with the token. ${RUN_NOTE}`}</Note>
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
