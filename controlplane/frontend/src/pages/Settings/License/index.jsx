import { useState, useEffect } from 'react'
import { Anchor, Group, JsonInput, Stack, Text, Title } from '@mantine/core'
import { AlertCircle } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import Table from '@/components/Table'
import TextInput from '@/components/TextInput'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useUserStore } from '@/stores/useUserStore'
import licenseService from '@/services/license'
import authService from '@/services/auth'
import { docsUrl } from '@/utils/docsUrl'
import { LICENSE_STATUS, daysUntilExpiration, formatLicenseDate } from '@/utils/license'
import { showSnackbar } from '@/utils/snackbar'
import { openSupport } from '@/utils/support'

// Wider than the global banner's 30 days: renewal happens here.
const EXPIRATION_WARNING_DAYS = 90
const SUPPORT_MESSAGE = 'I want to renew my hoop license'

function licenseTypeLabel(type) {
  if (type === 'enterprise') return 'Enterprise License'
  if (type === 'oss') return 'Open Source License'
  return '—'
}

// Covers an already expired license too, which the old check suppressed.
function expirationNotice(licenseInfo, isAdmin) {
  if (!isAdmin || !licenseInfo) return null
  const { status, type, expire_at: expireAt, verify_error: verifyError } = licenseInfo

  if (status === LICENSE_STATUS.EXPIRED) {
    return {
      color: 'red',
      message: `Your license expired on ${formatLicenseDate(expireAt)}. New sessions are blocked.`,
    }
  }
  if (status === LICENSE_STATUS.INVALID) {
    const reason = verifyError ? `: ${verifyError}` : ''
    return {
      color: 'red',
      message: `Your license could not be verified${reason}. New sessions are blocked.`,
    }
  }

  if (type !== 'enterprise') return null
  const daysLeft = daysUntilExpiration(expireAt)
  if (daysLeft === null || daysLeft > EXPIRATION_WARNING_DAYS) return null
  return {
    color: 'amber',
    message: `Your license expires on ${formatLicenseDate(expireAt)}. Renew it to avoid interruption.`,
  }
}

// Same page as webapp_v2's Settings > License. Not gated by licenseFeature:
// this is the page that installs the license.
function SettingsLicense() {
  const { isAdmin, setServerInfo } = useUserStore()
  const [licenseInfo, setLicenseInfo] = useState(null)
  const [licenseKey, setLicenseKey] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const showLoader = useMinDelay(loading)

  const disableInput = licenseInfo?.is_valid && licenseInfo?.type === 'enterprise'
  const disableSave = disableInput || !licenseKey.trim()
  const notice = expirationNotice(licenseInfo, isAdmin)

  useEffect(() => {
    licenseService
      .getInfo()
      .then(setLicenseInfo)
      .catch(() =>
        showSnackbar({ level: 'error', text: 'Failed to load license information' })
      )
      .finally(() => setLoading(false))
  }, [])

  async function handleSave() {
    let parsed
    try {
      parsed = JSON.parse(licenseKey)
    } catch {
      showSnackbar({ level: 'error', text: 'Error processing license: invalid JSON format' })
      return
    }

    setSaving(true)
    try {
      await licenseService.update(parsed)
      showSnackbar({ level: 'success', text: 'License updated successfully' })
      const [updated, serverInfo] = await Promise.all([licenseService.getInfo(), authService.getServerInfo()])
      setLicenseInfo(updated)
      setServerInfo(serverInfo)
      setLicenseKey('')
    } catch {
      showSnackbar({ level: 'error', text: 'Failed to update license' })
    } finally {
      setSaving(false)
    }
  }

  if (showLoader) return <PageLoader h={400} />

  return (
    <Stack gap={0}>
      <Group justify="space-between" align="flex-start" mb="xxxlAlt">
        <Stack gap="xs">
          <Title order={1}>License</Title>
          <Text size="lg" c="dimmed">
            {"View and manage your organization's license."}
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

      {notice && (
        <Alert icon={<AlertCircle size={16} />} color={notice.color} mb="xl">
          <Group gap="xs" align="center" wrap="wrap">
            <Text size="sm" component="span">
              {notice.message}
            </Text>
            <Anchor
              component="button"
              type="button"
              onClick={() => openSupport(SUPPORT_MESSAGE)}
              c={notice.color}
              fw={500}
              size="sm"
            >
              {'Contact support ↗'}
            </Anchor>
          </Group>
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
              <Table.Td>{formatLicenseDate(licenseInfo?.issued_at)}</Table.Td>
              <Table.Td>
                {licenseInfo?.type === 'oss' ? (
                  <Text size="xs" c="dimmed">N/A</Text>
                ) : (
                  formatLicenseDate(licenseInfo?.expire_at)
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
          {'Need more information? Check out '}
          <Anchor href={docsUrl.setup.licenseManagement} target="_blank" size="xs">
            License Management documentation
          </Anchor>
          {' or '}
          <Anchor href="https://help.hoop.dev/" target="_blank" size="xs">
            contact us
          </Anchor>
          {'.'}
        </Text>
      </Stack>
    </Stack>
  )
}

export default SettingsLicense
