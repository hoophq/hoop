import { useState } from 'react'
import { Group, List, Stack, Text } from '@mantine/core'
import { Download } from 'lucide-react'
import Button from '@/components/Button'
import Checkbox from '@/components/Checkbox'
import Modal from '@/components/Modal'
import { downloadCredentialsFile } from '../wizard'

// The passwords are shown once, so the modal cannot be dismissed: only the
// confirmed checkbox closes it.
export default function RootPasswordsModal({ credentials, onFinish }) {
  const [confirmed, setConfirmed] = useState(false)

  return (
    <Modal
      opened={!!credentials}
      onClose={() => {}}
      withCloseButton={false}
      closeOnEscape={false}
      closeOnClickOutside={false}
      title="Save your new root passwords"
      size={400}
    >
      <Stack gap="lg">
        <Text size="sm">The following file provides your new resources root passwords with full administrative access.</Text>
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            Please:
          </Text>
          <List type="ordered" size="sm" spacing={4}>
            <List.Item>Save this file immediately</List.Item>
            <List.Item>Store all information in a secure password manager</List.Item>
            <List.Item>Do not share via email or any unsecured channel</List.Item>
          </List>
        </Stack>
        <Stack gap="xs" align="flex-start">
          <Text size="sm" fw={500}>
            Resources new root passwords:
          </Text>
          <Button variant="light" rightSection={<Download size={16} />} onClick={() => downloadCredentialsFile(credentials)}>
            Download File
          </Button>
        </Stack>
        <Text size="sm">
          <Text span fw={500}>
            {'Note: '}
          </Text>
          These passwords will NEVER be shown again. There is no way to retrieve it after leaving this page.
        </Text>
        <Checkbox
          checked={confirmed}
          onChange={(e) => setConfirmed(e.currentTarget.checked)}
          label="I confirm that I have securely saved this password"
        />
        <Group justify="flex-end">
          <Button disabled={!confirmed} onClick={onFinish}>
            Confirm and Finish
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
