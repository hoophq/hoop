import { Fragment } from 'react'
import { useNavigate } from 'react-router-dom'
import { Anchor, Group, Paper, ScrollArea, SimpleGrid, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { BookMarked, Cable, Monitor, SquareTerminal } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import Tabs from '@/components/Tabs'
import { credentialTypeLabel, docsUrl, entryBadges, markdownLinkParts } from './entries'

const ACCESS_METHODS = [
  {
    key: 'native',
    icon: Cable,
    title: 'Native Access',
    description: 'Access resources through Hoop using their native protocols',
  },
  {
    key: 'webapp',
    icon: Monitor,
    title: 'Web App',
    description: 'Access resources and execute commands directly from Web UI.',
  },
  {
    key: 'runbooks',
    icon: BookMarked,
    title: 'Runbooks',
    description: 'Execute securely git-based predefined scripts in your resources.',
  },
  {
    key: 'cli',
    icon: SquareTerminal,
    title: 'Hoop CLI',
    description: 'Access resources and execute commands natively in your favorite apps.',
  },
]

export function LinkedText({ text, ...props }) {
  return (
    <Text {...props}>
      {markdownLinkParts(text).map((part, i) =>
        typeof part === 'string' ? (
          <Fragment key={i}>{part}</Fragment>
        ) : (
          <Anchor key={i} href={part.href} target="_blank" rel="noreferrer" inherit>
            {part.text}
          </Anchor>
        ),
      )}
    </Text>
  )
}

function EntryBadges({ entry }) {
  return entryBadges(entry).map((badge) => (
    <Badge key={badge.text} color={badge.color} variant="filled">
      {badge.text}
    </Badge>
  ))
}

function OverviewTab({ entry }) {
  const methods = entry.setupGuide?.accessMethods
  // The CLI card shows only for a type without native access.
  const shown = ACCESS_METHODS.filter(({ key }) => methods?.[key] && !(key === 'cli' && methods.native))

  return (
    <Stack gap="lg">
      {entry.overview?.description && <LinkedText text={entry.overview.description} />}
      {methods && (
        <Stack gap="md">
          <Title order={3}>Connection Methods</Title>
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
            {shown.map(({ key, icon: Icon, title, description }) => (
              <Paper key={key} withBorder p="md">
                <Group gap="sm" wrap="nowrap" align="flex-start">
                  <ThemeIcon size="lg" variant="light" color="gray">
                    <Icon size={18} />
                  </ThemeIcon>
                  <Stack gap={2}>
                    <Title order={4}>{title}</Title>
                    <Text size="sm" c="dimmed">
                      {description}
                    </Text>
                  </Stack>
                </Group>
              </Paper>
            ))}
          </SimpleGrid>
        </Stack>
      )}
    </Stack>
  )
}

function SetupGuideTab({ credentials }) {
  return (
    <Stack gap="md">
      <Title order={3}>Configuration</Title>
      {credentials.map((credential) => (
        <Paper key={credential.name} withBorder p="md">
          <Stack gap="xs">
            <Group justify="space-between" gap="xs">
              <Group gap="xs">
                <Title order={4}>{credential.name}</Title>
                {credential.required && <Badge variant="light">Required</Badge>}
              </Group>
              <Badge variant="light" color="gray" tag>
                {credentialTypeLabel(credential.type)}
              </Badge>
            </Group>
            <LinkedText text={credential.description} size="sm" c="dimmed" />
          </Stack>
        </Paper>
      ))}
    </Stack>
  )
}

export default function DetailModal({ entry, opened, onClose, isAdmin }) {
  const navigate = useNavigate()
  if (!entry) return null
  const credentials = entry.resourceConfiguration?.credentials

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      size="xl"
      title={
        <Group gap="xs">
          {entry.name}
          <EntryBadges entry={entry} />
        </Group>
      }
    >
      <Stack gap="lg">
        {entry.description && <Text c="dimmed">{entry.description}</Text>}
        <Tabs defaultValue="overview">
          <Tabs.List>
            <Tabs.Tab value="overview">Overview</Tabs.Tab>
            {credentials && <Tabs.Tab value="setup-guide">Setup Guide</Tabs.Tab>}
          </Tabs.List>
          <Tabs.Panel value="overview" pt="lg">
            <ScrollArea.Autosize mah={400}>
              <OverviewTab entry={entry} />
            </ScrollArea.Autosize>
          </Tabs.Panel>
          {credentials && (
            <Tabs.Panel value="setup-guide" pt="lg">
              <ScrollArea.Autosize mah={400}>
                <SetupGuideTab credentials={credentials} />
              </ScrollArea.Autosize>
            </Tabs.Panel>
          )}
        </Tabs>
        <Group justify="flex-end" gap="sm">
          <Button variant="default" component="a" href={docsUrl(entry)} target="_blank" rel="noreferrer">
            View Docs
          </Button>
          {isAdmin && (
            <Button onClick={() => navigate(`/resources/new?type=${encodeURIComponent(entry.id)}`)}>
              Continue Setup
            </Button>
          )}
        </Group>
      </Stack>
    </Modal>
  )
}
