import { useEffect, useRef, useState } from 'react'
import { Box, Flex, Group, Image, Loader, Stack, Text } from '@mantine/core'
import { ChevronDown, ChevronUp } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import RuleTrafficBadge from '@/components/RuleTrafficBadge'
import { connectionsService } from '@/services/connections'
import { getConnectionIcon } from '@/utils/connectionIcons'
import { ruleEntityBadges } from '../helpers'
import classes from './RuleListItem.module.css'

function ConnectionsPanel({ connections, loading, onConfigureConnection }) {
  return (
    <Box
      px="lg"
      py="md"
      bg="white"
      className={classes.panel}
    >
      <Group align="flex-start" gap="xl" wrap="nowrap">
        <Stack gap={2} w={220} className={classes.panelLabel}>
          <Text fw={500}>Connected Resources</Text>
          <Text size="sm" c="dimmed">
            Resource Roles that are using this Data Masking rule.
          </Text>
        </Stack>
        <Box
          flex={1}
          className={classes.connections}
        >
          {loading ? (
            <Flex p="md" align="center" justify="center">
              <Loader size="sm" />
            </Flex>
          ) : connections.length === 0 ? (
            <Flex p="md" align="center" justify="center">
              <Text size="sm" c="dimmed">
                No Resource Roles use this rule.
              </Text>
            </Flex>
          ) : (
            connections.map((connection) => (
              <Flex
                key={connection.id ?? connection.name}
                p="sm"
                align="center"
                justify="space-between"
                className={classes.connection}
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
                  size="sm"
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
  traffic,
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
      bg={showConnections ? 'var(--hoop-surface-muted)' : undefined}
    >
      <Flex p="lg" align="center" justify="space-between" gap="md">
        <Stack gap="xs" flex={1} miw={0}>
          <Group gap="sm">
            <Text fw={500} fz="lg">
              {rule.name}
            </Text>
            {traffic && <RuleTrafficBadge traffic={traffic} />}
          </Group>
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
              size="sm"
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
          {/* Hoop-managed rules (protection profiles) are immutable — the API
              rejects updates, so there is nothing to configure. */}
          {!rule.managed_by && (
            <Button variant="default" onClick={() => onConfigure(rule.id)}>
              Configure
            </Button>
          )}
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
