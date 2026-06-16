import { useEffect, useRef, useState } from 'react'
import { Box, Flex, Group, Image, Loader, Stack, Text } from '@mantine/core'
import { ChevronDown, ChevronUp } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { connectionsService } from '@/services/connections'
import { getConnectionIcon } from '@/utils/connectionIcons'
import { ruleEntityBadges } from '../helpers'

const BORDER = '1px solid var(--mantine-color-default-border)'

function ConnectionsPanel({ connections, loading, onConfigureConnection }) {
  return (
    <Box
      px="lg"
      py="md"
      bg="white"
      style={{
        borderTop: BORDER,
        borderBottomLeftRadius: 'var(--mantine-radius-md)',
        borderBottomRightRadius: 'var(--mantine-radius-md)',
      }}
    >
      <Group align="flex-start" gap="xl" wrap="nowrap">
        <Stack gap={2} w={220} style={{ flexShrink: 0 }}>
          <Text fw={500}>Connected Resources</Text>
          <Text size="sm" c="dimmed">
            Resource Roles that are using this Live Data Masking rule.
          </Text>
        </Stack>
        <Box
          flex={1}
          style={{ border: BORDER, borderRadius: 'var(--mantine-radius-md)' }}
        >
          {loading ? (
            <Flex p="md" align="center" justify="center">
              <Loader size="sm" />
            </Flex>
          ) : connections.length === 0 ? (
            <Flex p="md" align="center" justify="center">
              <Text size="sm" c="dimmed">
                No connections found for this rule
              </Text>
            </Flex>
          ) : (
            connections.map((connection, idx) => (
              <Flex
                key={connection.id ?? connection.name}
                p="sm"
                align="center"
                justify="space-between"
                style={
                  idx === connections.length - 1
                    ? undefined
                    : { borderBottom: BORDER }
                }
              >
                <Group gap="xs" align="center" wrap="nowrap" miw={0}>
                  <Image
                    src={getConnectionIcon(connection)}
                    w={20}
                    h={20}
                    fit="contain"
                    alt=""
                  />
                  <Text size="sm" lineClamp={1}>
                    {connection.name}
                  </Text>
                </Group>
                <Button
                  size="xs"
                  variant="default"
                  onClick={() => onConfigureConnection(connection.name)}
                >
                  Configure
                </Button>
              </Flex>
            ))
          )}
        </Box>
      </Group>
    </Box>
  )
}

export default function RuleListItem({
  rule,
  isFirst,
  isLast,
  onConfigure,
  onConfigureConnection,
}) {
  const [showConnections, setShowConnections] = useState(false)
  const [connections, setConnections] = useState(null)
  const fetchingRef = useRef(false)
  const badges = ruleEntityBadges(rule)
  const hasConnections = (rule.connection_ids ?? []).length > 0

  useEffect(() => {
    if (!showConnections || connections !== null || fetchingRef.current) return
    fetchingRef.current = true
    connectionsService
      .getConnectionsByIds(rule.connection_ids ?? [])
      .then((rows) => setConnections(rows ?? []))
      .catch(() => setConnections([]))
      .finally(() => {
        fetchingRef.current = false
      })
  }, [showConnections, connections, rule.connection_ids])

  return (
    <Box
      style={{
        borderLeft: BORDER,
        borderRight: BORDER,
        borderTop: isFirst ? BORDER : undefined,
        borderBottom: BORDER,
        borderTopLeftRadius: isFirst ? 'var(--mantine-radius-md)' : undefined,
        borderTopRightRadius: isFirst ? 'var(--mantine-radius-md)' : undefined,
        borderBottomLeftRadius: isLast ? 'var(--mantine-radius-md)' : undefined,
        borderBottomRightRadius: isLast ? 'var(--mantine-radius-md)' : undefined,
      }}
      bg={showConnections ? 'var(--mantine-color-gray-0)' : undefined}
    >
      <Flex p="lg" align="center" justify="space-between" gap="md">
        <Stack gap="xs" flex={1} miw={0}>
          <Text fw={500} fz="lg">
            {rule.name}
          </Text>
          {rule.description && (
            <Text size="sm" c="dimmed">
              {rule.description}
            </Text>
          )}
          {badges.length > 0 && (
            <Group gap="xs" wrap="wrap" pt={4}>
              {badges.map((label, idx) => (
                <Badge key={`${label}-${idx}`} variant="light" color="indigo">
                  {label}
                </Badge>
              ))}
            </Group>
          )}
        </Stack>
        <Group gap="sm" wrap="nowrap">
          {hasConnections && (
            <Button
              size="xs"
              variant="subtle"
              color="gray"
              onClick={() => setShowConnections((v) => !v)}
              rightSection={
                showConnections ? (
                  <ChevronUp size={14} />
                ) : (
                  <ChevronDown size={14} />
                )
              }
            >
              Resource Roles
            </Button>
          )}
          <Button variant="default" onClick={() => onConfigure(rule.id)}>
            Configure
          </Button>
        </Group>
      </Flex>
      {showConnections && (
        <ConnectionsPanel
          connections={connections ?? []}
          loading={connections === null}
          onConfigureConnection={onConfigureConnection}
        />
      )}
    </Box>
  )
}
