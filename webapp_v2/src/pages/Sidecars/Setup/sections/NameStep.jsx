import { useState } from 'react'
import { Anchor, Grid, Group, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { Link } from 'react-router-dom'
import Button from '@/components/Button'
import CodeSnippet from '@/components/CodeSnippet'
import Tabs from '@/components/Tabs'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'

function NumberedBlock({ n, title, children }) {
  return (
    <Group align="flex-start" gap="md" wrap="nowrap">
      <ThemeIcon size={32} radius="xl" variant="default">
        <Text size="sm" fw={700}>
          {n}
        </Text>
      </ThemeIcon>
      <Stack gap="sm" flex={1} miw={0}>
        <Text fw={600}>{title}</Text>
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
          <CodeSnippet code={s.code} variant={s.variant ?? 'gray'} />
        </Tabs.Panel>
      ))}
    </Tabs>
  )
}

// The container shapes the documentation publishes for the sidecar
// (Config File Reference → Components → "As a sidecar container" and
// "On Kubernetes"). The image is built from the Dockerfile in the repository;
// there is no published sidecar image to name here.
const DOCKER_COMPOSE = `hoop-inspect:
  image: hoop-inspect:local
  volumes:
    - ./config.yaml:/etc/hoop-inspect/config.yaml:ro
  ports:
    - "19000:19000"    # admin only
  healthcheck:
    test: ["CMD-SHELL", "curl -sf http://127.0.0.1:19000/healthz || exit 1"]`

const KUBERNETES = `containers:
  - name: hoop-inspect
    image: hoop-inspect:local
    env:
      - name: HOOP_SIDECAR_CONFIG
        value: /etc/hoop-inspect/config.yaml
    volumeMounts:
      - name: config
        mountPath: /etc/hoop-inspect
volumes:
  - name: config
    configMap:
      name: hoop-inspect-config`

/**
 * Step 1 of the sidecar wizard. The control plane issues a token for a named
 * sidecar (POST /sidecars), so the name comes first; the blocks that follow
 * are the documented steps for connecting one, in the documentation's order:
 * point it at the control plane, take the token, start it.
 */
export default function NameStep({ mode, sidecar, token, controlPlaneUrl, creating, error, onCreate }) {
  const [name, setName] = useState('')
  const created = !!sidecar
  const isConnect = mode === 'connect'

  const handleSubmit = (e) => {
    e.preventDefault()
    if (created || !name.trim()) return
    onCreate(name.trim())
  }

  // The deploy block only exists in the "create and deploy" flow; everything
  // after it is shared, so the numbering is computed rather than written.
  const blocks = []
  if (!isConnect) {
    blocks.push(
      <NumberedBlock key="deploy" n={blocks.length + 1} title="Deploy the sidecar">
        <SourceTabs
          sources={[
            { value: 'compose', label: 'Docker Compose', code: DOCKER_COMPOSE },
            { value: 'k8s', label: 'Kubernetes', code: KUBERNETES },
          ]}
        />
        <Text size="xs" c="dimmed">
          {'On Kubernetes, mount the config as a ConfigMap and set HOOP_SIDECAR_CONFIG instead of passing a flag. '}
          <Anchor href={docsUrl.sidecar.getStarted} target="_blank" rel="noopener noreferrer" size="xs">
            Running the Sidecar
          </Anchor>
          {' walks through the config file itself.'}
        </Text>
      </NumberedBlock>
    )
  }

  blocks.push(
    <NumberedBlock key="url" n={blocks.length + 1} title="Point the sidecar at the control plane">
      <SourceTabs
        sources={[
          { value: 'file', label: 'config.yaml', code: `control_plane_url: ${controlPlaneUrl}` },
          { value: 'env', label: 'Environment Variables', code: `export HOOP_CONTROL_PLANE_URL=${controlPlaneUrl}` },
        ]}
      />
      <Text size="xs" c="dimmed">
        The environment variable outranks the config file.
      </Text>
    </NumberedBlock>
  )

  blocks.push(
    <NumberedBlock key="token" n={blocks.length + 1} title="Copy the sidecar token">
      <CodeSnippet code={token} />
      <Text size="xs" c="dimmed">
        <Text component="span" fw={700}>
          Shown once.
        </Text>
        {
          ' There is no config file key for the token: it is a bearer credential, and a config file is the thing most likely to be committed. Leaving this page discards it; delete the sidecar to get a new one.'
        }
      </Text>
    </NumberedBlock>
  )

  blocks.push(
    <NumberedBlock key="start" n={blocks.length + 1} title={isConnect ? 'Restart your sidecar with the token' : 'Start your sidecar with the token'}>
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
          'Run this on the host that reaches your resources. Listeners come from its local file; guardrails, masking and analyzer settings arrive from the control plane.'
        }
      </Text>
    </NumberedBlock>
  )

  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Title order={4}>Connect it to this control plane</Title>
          <Text size="sm" c="dimmed">
            {isConnect
              ? 'Name the sidecar you already run to issue its token, then work through each block in order.'
              : 'Name the sidecar you are about to deploy to issue its token, then work through each block in order.'}
          </Text>
          <Anchor href={docsUrl.sidecar.connectToControlPlane} target="_blank" rel="noopener noreferrer" size="xs">
            Connect a Sidecar
          </Anchor>
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
                <Button type="submit" loading={creating} disabled={!name.trim()}>
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

          {created && <Stack gap="xl">{blocks}</Stack>}
        </Stack>
      </Grid.Col>
    </Grid>
  )
}
