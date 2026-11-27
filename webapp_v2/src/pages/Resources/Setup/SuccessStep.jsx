import { useNavigate } from 'react-router-dom'
import { Box, Center, Group, Image, Stack, Text, Title } from '@mantine/core'
import { AlertCircle, Cable, Monitor, PackagePlus } from 'lucide-react'
import ActionCard from '@/components/ActionCard'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { useBridgeStore } from '@/stores/useBridgeStore'
import { canOpenWebTerminal } from '@/utils/connectionPolicy'
import ActivationCards from './sections/ActivationCards'
import { useResourceSetupStore } from './store'

// `onboarding` adds an exit: the onboarding has no sidebar.
export default function SuccessStep({ onboarding = false }) {
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
                Every Resource Role will be authenticated, audited, and secured.
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
                <Text key={role.id} size="sm">
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
            badge={
              <Badge variant="light" color="indigo" tag chip>
                Recommended
              </Badge>
            }
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

      {onboarding && (
        <Center>
          <Button variant="default" onClick={() => navigate('/resources')}>
            Go to Resources
          </Button>
        </Center>
      )}
    </Stack>
  )
}
