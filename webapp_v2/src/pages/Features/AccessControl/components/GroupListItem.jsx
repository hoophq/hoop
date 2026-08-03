import { useState } from 'react'
import { Box, Flex, Grid, Group, Image, Stack, Text, Title } from '@mantine/core'
import { ChevronDown, ChevronUp } from 'lucide-react'
import Button from '@/components/Button'
import classes from './GroupListItem.module.css'

function ConnectionsPanel({ connections, onConfigureConnection }) {
  return (
    <Box px="xl" py="lg" className={classes.panel}>
      <Grid columns={7} gutter="xl">
        <Grid.Col span={2}>
          <Stack gap="xs">
            <Title order={4} fw={500}>
              Resources
            </Title>
            <Text size="sm" c="dimmed">
              These resource roles can be accessed by this user group.
            </Text>
          </Stack>
        </Grid.Col>
        <Grid.Col span={5}>
          <Box className={classes.connections} h="fit-content">
            {connections.map((connection) => (
              <Flex
                key={connection.id}
                p="xs"
                align="center"
                justify="space-between"
                gap="sm"
                className={classes.connection}
              >
                <Group gap="xs" align="center" wrap="nowrap" miw={0}>
                  <Image src={connection.iconUrl} alt="" w={16} h={16} miw={16} fit="contain" />
                  <Text size="sm" truncate>
                    {connection.name}
                  </Text>
                </Group>
                <Button
                  variant="default"
                  size="xs"
                  onClick={() => onConfigureConnection(connection.name)}
                >
                  Configure
                </Button>
              </Flex>
            ))}
          </Box>
        </Grid.Col>
      </Grid>
    </Box>
  )
}

// One row of the access control list. Rows stack into a single bordered block,
// so only the first and last ones carry the outer corners. Expanding a row
// reveals the resource roles the group can reach.
export default function GroupListItem({
  group,
  isFirst,
  isLast,
  onConfigure,
  onConfigureConnection,
}) {
  const [expanded, setExpanded] = useState(false)
  const hasConnections = group.connections.length > 0

  return (
    <Box
      className={classes.row}
      data-first={isFirst || undefined}
      data-last={isLast || undefined}
      data-expanded={expanded || undefined}
    >
      <Flex p="lg" align="center" justify="space-between" gap="md">
        <Text fw={500} fz="lg">
          {group.name}
        </Text>

        <Group align="center" gap="lg" wrap="nowrap">
          <Button variant="default" onClick={() => onConfigure(group.name)}>
            Configure
          </Button>

          {hasConnections && (
            <Button
              variant="subtle"
              color="gray"
              size="compact-sm"
              onClick={() => setExpanded((value) => !value)}
              rightSection={
                expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />
              }
            >
              Resource Roles
            </Button>
          )}
        </Group>
      </Flex>

      {expanded && (
        <ConnectionsPanel
          connections={group.connections}
          onConfigureConnection={onConfigureConnection}
        />
      )}
    </Box>
  )
}
