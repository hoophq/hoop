import { useState } from 'react'
import { Group, Loader, Stack, Text } from '@mantine/core'
import { Building2 } from 'lucide-react'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import { orgsService } from '@/services/orgs'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'

const RELOAD_DELAY_MS = 2500

function Migrating({ orgName }) {
  return (
    <Stack align="center" gap="md" py="xl">
      <Loader />
      <Stack align="center" gap={4}>
        <Text fw={600} ta="center">
          Migrating your organization
        </Text>
        <Text size="sm" c="dimmed" ta="center">
          {'Setting up your account in '}
          <Text span fw={600} inherit>
            {orgName}
          </Text>
          {'. This may take a moment.'}
        </Text>
      </Stack>
    </Stack>
  )
}

// The CLJS org-migration-dialog, from /userinfo pending_org_invitations. The
// person must answer it: no close button, no outside click, no Escape.
export default function OrgInvitationDialog() {
  const invitation = useUserStore((s) => s.user?.pending_org_invitations?.[0])
  const clearPendingOrgInvitations = useUserStore((s) => s.clearPendingOrgInvitations)
  const [migrating, setMigrating] = useState(false)
  const [declining, setDeclining] = useState(false)

  if (!invitation) return null
  const orgName = invitation.org_name

  const accept = () => {
    setMigrating(true)
    orgsService
      .respondToInvitation('accept')
      .then(() => setTimeout(() => window.location.reload(), RELOAD_DELAY_MS))
      .catch((err) => {
        setMigrating(false)
        showSnackbar({
          level: 'error',
          text: 'Failed to migrate the organization.',
          description: err.response?.data?.message,
        })
      })
  }

  const decline = () => {
    setDeclining(true)
    orgsService
      .respondToInvitation('decline')
      .catch(() => {})
      .finally(() => {
        setDeclining(false)
        clearPendingOrgInvitations()
      })
  }

  return (
    <Modal
      opened
      onClose={() => {}}
      withCloseButton={false}
      closeOnClickOutside={false}
      closeOnEscape={false}
      size={500}
      title={
        migrating ? null : (
          <Group gap="xs">
            <Building2 size={20} />
            Organization invitation
          </Group>
        )
      }
    >
      {migrating ? (
        <Migrating orgName={orgName} />
      ) : (
        <Stack>
          <Text size="sm">
            {'You have been invited to join '}
            <Text span fw={600} inherit>
              {orgName}
            </Text>
            {'. Would you like to migrate to this organization? Your current organization will be removed.'}
          </Text>
          <Group justify="flex-end" mt="xs">
            <Button variant="subtle" color="gray" size="sm" onClick={decline} loading={declining}>
              Stay in current org
            </Button>
            <Button size="sm" onClick={accept} disabled={declining}>
              {`Join ${orgName}`}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}
