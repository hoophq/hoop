import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import { FileText } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Code from '@/components/Code'
import FileInput from '@/components/FileInput'
import Switch from '@/components/Switch'
import Table from '@/components/Table'
import { usersService } from '@/services/users'
import { showSnackbar } from '@/utils/snackbar'

function message(error) {
  return error?.response?.data?.message || error?.message
}

/**
 * The control plane's file import of users and groups (ADR-0019): a CSV with
 * the columns email,name,groups, groups separated by ";". Each group the file
 * names gets exactly the users that list it. A bad row fails that row only,
 * and the result lists it.
 *
 * Used by Settings -> Provisioning (File tab) and the Users page.
 */
export default function UserImportForm({ disabled, onImported }) {
  const [file, setFile] = useState(null)
  const [deactivateMissing, setDeactivateMissing] = useState(false)
  const [importing, setImporting] = useState(false)
  const [result, setResult] = useState(null)

  async function handleImport() {
    if (!file) return
    setImporting(true)
    try {
      const { data } = await usersService.importFile(file, deactivateMissing)
      setResult(data)
      const failed = data?.errors?.length ?? 0
      showSnackbar({
        level: failed ? 'info' : 'success',
        text: failed ? `Imported with ${failed} failed row(s).` : 'Users imported.',
      })
      onImported?.(data)
    } catch (error) {
      showSnackbar({ level: 'error', text: 'Failed to import the file.', description: message(error) })
    } finally {
      setImporting(false)
    }
  }

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        {'One user per row with the header '}
        <Code>email,name,groups</Code>
        {'. Separate groups with ";". The admin group cannot be named.'}
      </Text>
      <FileInput
        label="CSV file"
        placeholder="Drop a CSV file or click to choose one"
        accept=".csv,text/csv"
        value={file}
        onChange={(f) => {
          setFile(f)
          setResult(null)
        }}
        disabled={disabled}
      />
      <Switch
        label="Deactivate users missing from the file"
        description="Only users a previous file import created. Administrators are never deactivated."
        checked={deactivateMissing}
        onChange={(e) => setDeactivateMissing(e.currentTarget.checked)}
        disabled={disabled}
      />
      <Group justify="flex-end">
        <Button
          leftSection={<FileText size={16} />}
          onClick={handleImport}
          loading={importing}
          disabled={disabled || !file}
        >
          Import
        </Button>
      </Group>

      {result && (
        <Stack gap="xs">
          <Text size="sm">
            {`Created ${result.created}, updated ${result.updated}, deactivated ${result.deactivated}.`}
          </Text>
          {result.errors?.length > 0 && (
            <Alert color="red" variant="light" radius="md" title="Rows not imported">
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th w={60}>Row</Table.Th>
                    <Table.Th>Email</Table.Th>
                    <Table.Th>Reason</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {result.errors.map((e) => (
                    <Table.Tr key={e.row}>
                      <Table.Td>{e.row}</Table.Td>
                      <Table.Td>{e.email || '—'}</Table.Td>
                      <Table.Td>{e.message}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Alert>
          )}
        </Stack>
      )}
    </Stack>
  )
}
