import { useState, useEffect } from 'react'
import {
  Alert,
  Anchor,
  Button,
  Group,
  JsonInput,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { AlertCircle } from 'lucide-react'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useUserStore } from '@/stores/useUserStore'
import PageLoader from '@/components/PageLoader'
import Table from '@/components/Table'
import licenseService from '@/services/license'
import authService from '@/services/auth'
import { docsUrl } from '@/utils/docsUrl'

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

function formatDate(timestamp) {
  if (!timestamp) return '—'
  const date = new Date(timestamp * 1000)
  const day = String(date.getDate()).padStart(2, '0')
  return `${date.getFullYear()}/${MONTHS[date.getMonth()]}/${day}`
}

function licenseTypeLabel(type) {
  if (type === 'enterprise') return 'Enterprise License'
  if (type === 'oss') return 'Open Source License'
  return '—'
}

function shouldShowExpirationWarning(licenseInfo, isAdmin) {
  if (!isAdmin) return false
  if (licenseInfo?.type !== 'enterprise') return false
  if (!licenseInfo?.is_valid) return false
  if (!licenseInfo?.expire_at) return false
  const now = Date.now()
  const expireMs = licenseInfo.expire_at * 1000
  const ninetyDaysMs = 90 * 24 * 60 * 60 * 1000
  return now >= expireMs - ninetyDaysMs
}

function SettingsLicense() {
  const { isAdmin, setServerInfo } = useUserStore()
  const [licenseInfo, setLicenseInfo] = useState(null)
  const [licenseKey, setLicenseKey] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const showLoader = useMinDelay(loading)

  const disableInput = licenseInfo?.is_valid && licenseInfo?.type === 'enterprise'
  const disableSave = disableInput || !licenseKey.trim()

  useEffect(() => {
    licenseService
      .getInfo()
      .then(setLicenseInfo)
      .catch(() =>
        notifications.show({ color: 'red', title: 'Error', message: 'Failed to load license information' })
      )
      .finally(() => setLoading(false))
  }, [])

  async function handleSave() {
    let parsed
    try {
      parsed = JSON.parse(licenseKey)
    } catch {
      notifications.show({ color: 'red', title: 'Error', message: 'Error processing license: invalid JSON format' })
      return
    }

    setSaving(true)
    try {
      await licenseService.update(parsed)
      notifications.show({ color: 'green', title: 'Saved', message: 'License updated successfully' })
      const [updated, serverInfo] = await Promise.all([licenseService.getInfo(), authService.getServerInfo()])
      setLicenseInfo(updated)
      setServerInfo(serverInfo)
      setLicenseKey('')
    } catch {
      notifications.show({ color: 'red', title: 'Error', message: 'Failed to update license' })
    } finally {
      setSaving(false)
    }
  }

  if (showLoader) return <PageLoader h={400} />

  return (
    <Stack gap={0}>
      <Group justify="space-between" align="flex-start" mb="xxxl">
        <Stack gap="xs">
          <Title order={1}>License</Title>
          <Text size="lg" c="dimmed">
            View and manage your organization's license.
          </Text>
        </Stack>
        <Group gap="md">
          <Button
            variant="subtle"
            color="gray"
            component="a"
            href="https://help.hoop.dev/"
            target="_blank"
            rel="noopener noreferrer"
          >
            Contact us
          </Button>
          <Button loading={saving} disabled={disableSave} onClick={handleSave}>
            Save
          </Button>
        </Group>
      </Group>

      {shouldShowExpirationWarning(licenseInfo, isAdmin) && (
        <Alert icon={<AlertCircle size={16} />} color="yellow" mb="xl">
          Your organization's license is expiring soon. Please contact us to avoid interruption.
        </Alert>
      )}

      <Stack gap="xl">
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Type</Table.Th>
              <Table.Th>Issued</Table.Th>
              <Table.Th>Expiration</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            <Table.Tr>
              <Table.Td>{licenseTypeLabel(licenseInfo?.type)}</Table.Td>
              <Table.Td>{formatDate(licenseInfo?.issued_at)}</Table.Td>
              <Table.Td>
                {licenseInfo?.type === 'oss' ? (
                  <Text size="xs" c="dimmed">N/A</Text>
                ) : (
                  formatDate(licenseInfo?.expire_at)
                )}
              </Table.Td>
            </Table.Tr>
          </Table.Tbody>
        </Table>

        <Stack gap="xs">
          <Title order={4}>License Details</Title>
          <Table>
            <Table.Tbody>
              <Table.Tr>
                <Table.Th w="30%">Verified Hostname</Table.Th>
                <Table.Td>{licenseInfo?.verified_host}</Table.Td>
              </Table.Tr>
              <Table.Tr>
                <Table.Th>Enterprise License</Table.Th>
                <Table.Td>
                  {licenseInfo?.key_id || <Text size="xs" c="dimmed">N/A</Text>}
                </Table.Td>
              </Table.Tr>
              <Table.Tr>
                <Table.Th>License Key</Table.Th>
                <Table.Td>
                  {disableInput ? (
                    <TextInput value="•••••••••••••••••" disabled />
                  ) : (
                    <JsonInput
                      value={licenseKey}
                      onChange={setLicenseKey}
                      placeholder="Paste your license key JSON here"
                      validationError="Invalid JSON format"
                      formatOnBlur
                      autosize
                      minRows={4}
                    />
                  )}
                </Table.Td>
              </Table.Tr>
            </Table.Tbody>
          </Table>
        </Stack>

        <Text size="xs" c="dimmed" ta="center">
          Need more information? Check out{' '}
          <Anchor href={docsUrl.setup.licenseManagement} target="_blank" size="xs">
            License Management documentation
          </Anchor>
          {' '}or{' '}
          <Anchor href="https://help.hoop.dev/" target="_blank" size="xs">
            contact us
          </Anchor>
          .
        </Text>
      </Stack>
    </Stack>
  )
}

export default SettingsLicense
