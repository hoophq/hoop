import { useState } from 'react'
import { Anchor, Grid, Group, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { Link } from 'react-router-dom'
import Button from '@/components/Button'
import CodeSnippet from '@/components/CodeSnippet'
import TextInput from '@/components/TextInput'

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
 * Step 1 of the sidecar wizard. The gateway issues the token for a named
 * sidecar (POST /sidecars), so the name comes first; the values the sidecar
 * needs follow, each with a copy button. The token is shown here and nowhere
 * else (Figma: "Connect an existing Sidecar | Connect").
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

  return (
    <Grid gutter="xxl">
      <Grid.Col span={{ base: 12, md: 4 }}>
        <Stack gap="xs">
          <Title order={4}>Add a configuration</Title>
          <Text size="sm" c="dimmed">
            {isConnect
              ? 'Name the sidecar you already run to get its values. Copy each block in order.'
              : 'Name the sidecar you are about to deploy to get its values. Copy each block in order.'}
          </Text>
        </Stack>
      </Grid.Col>

      <Grid.Col span={{ base: 12, md: 8 }}>
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

          {created && (
            <Stack gap="xl">
              <NumberedBlock n={1} title="Control plane URL">
                <CodeSnippet code={controlPlaneUrl} variant="gray" />
              </NumberedBlock>

              <NumberedBlock n={2} title="Sidecar token">
                <CodeSnippet code={token} />
                <Text size="xs" c="dimmed">
                  <Text component="span" fw={700}>
                    Shown once.
                  </Text>
                  {
                    ' Keep it in your secrets manager and hand it to the sidecar when it supports the control plane. Never write it to a file. Leaving this page discards it; delete the sidecar to get a new one.'
                  }
                </Text>
              </NumberedBlock>

              <NumberedBlock n={3} title={isConnect ? 'Restart your sidecar' : 'Start your sidecar'}>
                <Text size="sm" c="dimmed">
                  {isConnect
                    ? 'Run it on the host that reaches your resources.'
                    : 'Deploy it with Docker or Kubernetes on the host that reaches your resources.'}
                </Text>
              </NumberedBlock>
            </Stack>
          )}
        </Stack>
      </Grid.Col>
    </Grid>
  )
}
