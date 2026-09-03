import { useEffect, useState } from 'react'
import { Anchor, Group, Stack, Text, Title } from '@mantine/core'
import { AlertCircle, Info } from 'lucide-react'
import AddLicenseCta from '@/components/AddLicenseCta'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import Table from '@/components/Table'
import { RENEW_MESSAGE } from '@/features/License/constants'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { LICENSE_STATE, formatLicenseDate, licenseState } from '@/utils/license'
import { openSales, openSupport } from '@/utils/support'
import { licenseNotice, licenseTypeLabel } from './helpers'

const STATUS_BADGE = { valid: 'active', expired: 'warning', invalid: 'danger' }

/**
 * Settings > License. Reads license_info from the store and shows what the
 * gateway concluded: type, dates, status, the hostname it verified against and
 * the hosts the license allows. Installing or replacing a license happens in
 * the shared AddLicenseModal, opened by the header button.
 *
 * Not gated by licenseFeature on purpose: this is the page that fixes a
 * missing or invalid license.
 */
function SettingsLicense() {
  const licenseInfo = useUserStore((state) => state.licenseInfo)
  const serverInfoLoaded = useUserStore((state) => state.serverInfoLoaded)
  const refreshServerInfo = useUserStore((state) => state.refreshServerInfo)
  const [refreshFailed, setRefreshFailed] = useState(false)

  // ProtectedRoute loaded /serverinfo once. One more read on mount makes the
  // page authoritative after a license installed from the CLI.
  useEffect(() => {
    let active = true
    refreshServerInfo().then((ok) => {
      if (active && !ok) setRefreshFailed(true)
    })
    return () => {
      active = false
    }
  }, [refreshServerInfo])

  const state = licenseState(licenseInfo, serverInfoLoaded)

  if (state === LICENSE_STATE.UNKNOWN) {
    if (!refreshFailed) return <PageLoader h={400} />
    const retry = () => {
      setRefreshFailed(false)
      refreshServerInfo().then((ok) => {
        if (!ok) setRefreshFailed(true)
      })
    }
    return (
      <Stack align="center" gap="md">
        <PageLoader h={300} error message="Could not load license information." />
        <Button variant="default" onClick={retry}>
          Try again
        </Button>
      </Stack>
    )
  }

  const notice = licenseNotice(licenseInfo, state)
  const NoticeIcon = notice?.color === 'blue' ? Info : AlertCircle
  const isOss = licenseInfo.type === 'oss'
  const features = licenseInfo.features?.length ? licenseInfo.features.join(', ') : 'All features'
  const allowedHosts = licenseInfo.allowed_hosts?.length ? licenseInfo.allowed_hosts.join(', ') : '—'

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>License</Title>
          <Text size="md" c="dimmed">
            {"View and manage your organization's license."}
          </Text>
        </Stack>
        <Group gap="md">
          <Button variant="subtle" color="gray" onClick={() => openSupport(RENEW_MESSAGE)}>
            Contact support
          </Button>
          <AddLicenseCta />
        </Group>
      </Group>

      {notice && (
        <Alert color={notice.color} variant="light" radius="md" icon={<NoticeIcon size={16} />}>
          <Group gap="xs" align="center" wrap="wrap">
            <Text size="sm" component="span">
              {notice.message}
            </Text>
            <Anchor
              component="button"
              type="button"
              onClick={() => (notice.action === 'sales' ? openSales() : openSupport(RENEW_MESSAGE))}
              c={notice.color}
              fw={500}
              size="sm"
            >
              {notice.action === 'sales' ? 'Talk to sales ↗' : 'Contact support ↗'}
            </Anchor>
          </Group>
        </Alert>
      )}

      <Table>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Type</Table.Th>
            <Table.Th>Issued</Table.Th>
            <Table.Th>Expiration</Table.Th>
            <Table.Th>Status</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          <Table.Tr>
            <Table.Td>{licenseTypeLabel(licenseInfo.type)}</Table.Td>
            <Table.Td>{formatLicenseDate(licenseInfo.issued_at)}</Table.Td>
            <Table.Td>
              {isOss ? (
                <Text size="xs" c="dimmed">
                  N/A
                </Text>
              ) : (
                formatLicenseDate(licenseInfo.expire_at)
              )}
            </Table.Td>
            <Table.Td>
              <Badge variant={STATUS_BADGE[licenseInfo.status] ?? 'inactive'}>
                {licenseInfo.status ?? 'unknown'}
              </Badge>
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
              <Table.Td>{licenseInfo.verified_host || '—'}</Table.Td>
            </Table.Tr>
            <Table.Tr>
              <Table.Th>Allowed Hosts</Table.Th>
              <Table.Td>{allowedHosts}</Table.Td>
            </Table.Tr>
            <Table.Tr>
              <Table.Th>License ID</Table.Th>
              <Table.Td>
                {licenseInfo.key_id || (
                  <Text size="xs" c="dimmed">
                    N/A
                  </Text>
                )}
              </Table.Td>
            </Table.Tr>
            <Table.Tr>
              <Table.Th>Features</Table.Th>
              <Table.Td>{isOss ? '—' : features}</Table.Td>
            </Table.Tr>
          </Table.Tbody>
        </Table>
      </Stack>

      <Text size="xs" c="dimmed" ta="center">
        {'Need more information? Check out the '}
        <Anchor
          href={docsUrl.setup.licenseManagement}
          target="_blank"
          rel="noopener noreferrer"
          size="xs"
        >
          License Management documentation
        </Anchor>
        {'.'}
      </Text>
    </Stack>
  )
}

export default SettingsLicense
