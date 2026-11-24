import { useNavigate } from 'react-router-dom'
import { Box, Center, Group, Image, Paper, Stack, Text, Title, UnstyledButton } from '@mantine/core'
import { AlertCircle, Cable, ChevronRight, Monitor, PackagePlus } from 'lucide-react'
import Alert from '@/components/Alert'
import Avatar from '@/components/Avatar'
import Badge from '@/components/Badge'
import { useBridgeStore } from '@/stores/useBridgeStore'
import { canOpenWebTerminal } from '@/utils/connectionPolicy'
import ActivationCards from './sections/ActivationCards'
import { useResourceSetupStore } from './store'
import classes from './SuccessStep.module.css'

function ActionCard({ icon: Icon, title, badge, description, onClick }) {
  return (
    <UnstyledButton onClick={onClick} className={classes.actionCard}>
      <Paper withBorder radius="md" p="md" bg="transparent">
        <Group gap="md" wrap="nowrap">
          <Avatar size="lg" radius="md" color="gray" variant="light">
            <Icon size={20} />
          </Avatar>
          <Stack gap={2} flex={1}>
            <Group gap="xs">
              <Text fw={500}>{title}</Text>
              {badge && (
                <Badge variant="light" color="indigo" tag chip>
                  {badge}
                </Badge>
              )}
            </Group>
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          </Stack>
          <ChevronRight size={16} aria-hidden="true" />
        </Group>
      </Paper>
    </UnstyledButton>
  )
}

export default function SuccessStep() {
  const navigate = useNavigate()
  const setup = useResourceSetupStore((s) => s.setup)
  const createdRoles = useResourceSetupStore((s) => s.createdRoles)
  const failedRoles = useResourceSetupStore((s) => s.failedRoles)
  // Only one role can open the web terminal: the card opens that role.
  const terminalRoles = createdRoles.filter(canOpenWebTerminal)
  const terminalRole = terminalRoles.length === 1 ? terminalRoles[0] : null

  const openWebTerminal = () => {
    useBridgeStore.getState().clearDatabaseSchema()
    navigate('/client?role=' + encodeURIComponent(terminalRole.name))
    useBridgeStore.getState().syncPrimaryConnectionFromUrl()
  }

  return (
    <Stack gap="xxlAlt" py="xs" px={{ base: 0, md: 'xxxlAlt' }}>
      <Stack gap="lg" align="center">
        <Center>
          <Image src="/icons/icon-lock-big.svg" alt="Lock icon, success." w={80} h={80} />
        </Center>
        {failedRoles.length > 0 ? (
          <Stack gap="xs" align="center">
            <Title order={2}>Not every role was created</Title>
            <Text c="dimmed" ta="center">
              {`${createdRoles.length} of ${createdRoles.length + failedRoles.length} roles were created.`}
            </Text>
          </Stack>
        ) : (
          <Stack gap="xs" align="center">
            <Title order={2}>Your resource is ready</Title>
            <Stack gap={0} align="center">
              <Text c="dimmed" ta="center">
                {`Your ${setup.subtype} is now protected by the access gateway.`}
              </Text>
              <Text c="dimmed" ta="center">
                Every resource role will be authenticated, audited, and secured.
              </Text>
            </Stack>
          </Stack>
        )}
        <Group justify="center" gap="xs">
          {createdRoles.map((role) => (
            <Badge key={role.name} variant="filled" color="indigo" tag chip fullLabel icon={<Cable size={12} />}>
              {role.name}
            </Badge>
          ))}
        </Group>
        {failedRoles.length > 0 && (
          <Alert icon={<AlertCircle size={16} />} color="red" title="Not created" w="100%">
            <Stack gap="xs">
              {failedRoles.map((role) => (
                <Text key={role.name} size="sm">
                  <Text span fw={500} size="sm">
                    {role.name}
                  </Text>
                  {role.error ? `: ${role.error}` : ''}
                </Text>
              ))}
            </Stack>
          </Alert>
        )}
      </Stack>

      <Stack gap="md">
        <Title order={4}>Next steps:</Title>
        {terminalRole && (
          <ActionCard
            icon={Monitor}
            title="Test Connection on Web Terminal"
            badge="Recommended"
            description="Start using your resource immediately in your browser"
            onClick={openWebTerminal}
          />
        )}
        <ActionCard
          icon={PackagePlus}
          title="Add another resource"
          description="Set up a new resource from scratch"
          onClick={() => navigate('/resource-catalog')}
        />
      </Stack>

      <Box>
        <ActivationCards />
      </Box>
    </Stack>
  )
}
