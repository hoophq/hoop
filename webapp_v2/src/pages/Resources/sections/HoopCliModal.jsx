import { useState } from 'react'
import { Anchor, Box, Group, Image, ScrollArea, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import CodeSnippet from '@/components/CodeSnippet'
import Modal from '@/components/Modal'
import Tabs from '@/components/Tabs'
import { useUserStore } from '@/stores/useUserStore'

// Port of webapp/src/webapp/connections/views/hoop_cli_modal.cljs.
const DOCS = {
  macos: 'https://hoop.dev/docs/clients/cli#mac-os',
  linux: 'https://hoop.dev/docs/clients/cli#linux',
  windows: 'https://hoop.dev/docs/clients/cli#windows',
}

// CLJS caps the modal at 696px; 90dvh is the Mantine modal's own limit.
const MAX_HEIGHT = 'min(696px, 90dvh)'

function steps(os, apiUrl, connectionName) {
  const install =
    os === 'macos'
      ? 'brew tap hoophq/brew https://github.com/hoophq/brew\nbrew install hoop'
      : 'curl -s -L https://releases.hoop.dev/release/install-cli.sh | sh'
  return [
    { title: 'Install Hoop CLI', code: install },
    { title: 'Configure authentication', code: 'hoop config create --api-url ' + apiUrl },
    { title: 'Get an access token', code: 'hoop login' },
    { title: 'Connect to resource', code: 'hoop connect ' + connectionName },
  ]
}

function Steps({ os, apiUrl, connectionName }) {
  return (
    <Stack gap="lg">
      {steps(os, apiUrl, connectionName).map((step) => (
        <Stack key={step.title} gap="xs">
          <Title order={4}>{step.title}</Title>
          <CodeSnippet code={step.code} />
        </Stack>
      ))}
    </Stack>
  )
}

export default function HoopCliModal({ connectionName, onClose }) {
  const [os, setOs] = useState('macos')
  const apiUrl = useUserStore((s) => s.apiUrl) || window.location.origin

  return (
    <Modal opened={Boolean(connectionName)} onClose={onClose} size={1100} withCloseButton={false} padding={0}>
      <Group align="stretch" gap={0} wrap="nowrap">
        <ScrollArea.Autosize mah={MAX_HEIGHT} flex={1}>
          <Stack gap="xl" p="xl">
            <Stack gap="xs">
              <Title order={2}>Get more with Hoop CLI</Title>
              <Text size="sm" c="dimmed">
                Follow the steps below to connect to your resource via native access.
              </Text>
            </Stack>
            <Tabs value={os} onChange={(next) => next && setOs(next)}>
              <Tabs.List grow mb="lg">
                <Tabs.Tab value="macos">MacOS</Tabs.Tab>
                <Tabs.Tab value="linux">Linux</Tabs.Tab>
                <Tabs.Tab value="windows">Windows</Tabs.Tab>
              </Tabs.List>
              <Tabs.Panel value="macos">
                <Steps os="macos" apiUrl={apiUrl} connectionName={connectionName} />
              </Tabs.Panel>
              <Tabs.Panel value="linux">
                <Steps os="linux" apiUrl={apiUrl} connectionName={connectionName} />
              </Tabs.Panel>
              <Tabs.Panel value="windows">
                <Stack gap="xs">
                  <Title order={4}>Install Hoop CLI</Title>
                  <Text size="sm" c="dimmed">
                    {'For details about Windows installation check '}
                    <Anchor href={DOCS.windows} target="_blank" size="sm">
                      Windows CLI documentation
                    </Anchor>
                    {'.'}
                  </Text>
                </Stack>
              </Tabs.Panel>
            </Tabs>
            <Group justify="space-between">
              <Button variant="subtle" color="gray" onClick={onClose}>
                Close
              </Button>
              {os !== 'windows' && (
                <Button variant="subtle" color="gray" component="a" href={DOCS[os]} target="_blank">
                  Hoop CLI Docs
                </Button>
              )}
            </Group>
          </Stack>
        </ScrollArea.Autosize>
        {/* Absolute, so the picture takes the content's height instead of setting it. */}
        <Box w="45%" pos="relative" bg="indigo.0" visibleFrom="sm">
          <Image src="/images/illustrations/cli-promotion.png" alt="" pos="absolute" inset={0} w="100%" h="100%" fit="cover" />
        </Box>
      </Group>
    </Modal>
  )
}
