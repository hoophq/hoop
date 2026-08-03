import { Group, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { ListCheck, Settings2, UserRoundCheck } from 'lucide-react'
import Button from '@/components/Button'
import FeaturePromotion from '@/components/FeaturePromotion'
import Modal from '@/components/Modal'

const FEATURE_ITEMS = [
  {
    icon: <ListCheck size={20} />,
    title: 'Role-Based Access Control (RBAC)',
    description:
      'Granular permission management for resources with flexible role assignments and group management.',
  },
  {
    icon: <Settings2 size={20} />,
    title: 'Connection-Level Permissions',
    description:
      'Group-based access management with customizable access levels per connection.',
  },
  {
    icon: <UserRoundCheck size={20} />,
    title: 'Dynamic Access Management',
    description:
      'Real-time access updates and modifications with seamless integration with identity providers.',
  },
]

// Shown while the access_control plugin has never been enabled. Activating it
// flips the whole organization to deny-by-default, so it goes through a
// confirmation rather than firing on the primary click.
export default function AccessControlPromotion({ onActivate, activating }) {
  const [opened, modal] = useDisclosure(false)

  const handleConfirm = async () => {
    await onActivate()
    modal.close()
  }

  return (
    <>
      <FeaturePromotion
        featureName="Access Control"
        mode="empty-state"
        image="access-control-promotion.png"
        description="Transform your data management from unstructured to controlled with powerful permission rules."
        featureItems={FEATURE_ITEMS}
        onPrimaryClick={modal.open}
        primaryText="Activate Access Control"
      />

      <Modal opened={opened} onClose={modal.close} title="Activate Access Control">
        <Stack gap="lg">
          <Text size="sm">
            By activating this feature users will have their accesses blocked until a
            connection permission is set.
          </Text>
          <Group justify="flex-end" gap="sm">
            <Button variant="subtle" color="gray" onClick={modal.close}>
              Cancel
            </Button>
            <Button onClick={handleConfirm} loading={activating}>
              Confirm
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  )
}
