import { useEffect, useState, useRef } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Stack, Text, Modal, Group, Button } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import Tabs from '@/components/Tabs'
import PageLoader from '@/components/PageLoader'
import { useUserStore } from '@/stores/useUserStore'
import { useConfigureRoleStore } from './store'
import ConfigureHeader from './components/ConfigureHeader'
import FormFooter from './components/FormFooter'
import CredentialsTab from './components/CredentialsTab'
import PlaceholderTab from './components/PlaceholderTab'

function DeleteConfirmationModal({ opened, onClose, onConfirm, connectionName, deleting }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Delete role?" centered size="sm">
      <Stack>
        <Stack gap={4}>
          <Text size="sm">
            {'This action will instantly remove your access to ' +
              connectionName +
              ' and cannot be undone.'}
          </Text>
          <Text size="sm">Are you sure you want to delete this role?</Text>
        </Stack>
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={deleting}>
            Cancel
          </Button>
          <Button color="red" onClick={onConfirm} loading={deleting}>
            Confirm and delete
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

export default function ConfigureRolePage() {
  const navigate = useNavigate()
  const { connectionName } = useParams()
  const { user } = useUserStore()
  const isAdmin = !!user?.is_admin

  const {
    connection,
    loading,
    error,
    saving,
    deleting,
    testing,
    testResult,
    stagedSecrets,
    loadConnection,
    save,
    deleteConnection,
    runTestConnection,
    clearTestResult,
    reset,
  } = useConfigureRoleStore()

  const [tab, setTab] = useState('details')
  const [opened, { open, close }] = useDisclosure(false)
  const formRef = useRef(null)

  useEffect(() => {
    loadConnection(connectionName)
    return () => reset()
  }, [connectionName, loadConnection, reset])

  useEffect(() => {
    if (!testResult) return
    notifications.show({
      message: testResult.success
        ? 'Connection test succeeded.'
        : testResult.message || 'Connection test failed.',
      color: testResult.success ? 'green' : 'red',
    })
    clearTestResult()
  }, [testResult, clearTestResult])

  const handleSave = async () => {
    if (formRef.current && !formRef.current.checkValidity()) {
      // Move focus to Credentials tab where required inputs live.
      setTab('credentials')
      requestAnimationFrame(() => formRef.current?.reportValidity())
      return
    }
    try {
      await save()
      notifications.show({ message: 'Connection saved.', color: 'green' })
    } catch {
      notifications.show({ message: 'Failed to save connection.', color: 'red' })
    }
  }

  const handleDelete = async () => {
    try {
      await deleteConnection()
      close()
      notifications.show({
        message: 'Connection deleted.',
        color: 'green',
      })
      navigate('/resources')
    } catch {
      notifications.show({ message: 'Failed to delete connection.', color: 'red' })
    }
  }

  if (loading) {
    return <PageLoader h={400} />
  }
  if (error) {
    return <Text c="red">{error}</Text>
  }
  if (!connection) {
    return null
  }

  const dirty = Object.keys(stagedSecrets).length > 0

  return (
    <>
      <DeleteConfirmationModal
        opened={opened}
        onClose={close}
        onConfirm={handleDelete}
        connectionName={connection.name}
        deleting={deleting}
      />

      <form
        ref={formRef}
        onSubmit={(e) => {
          e.preventDefault()
          handleSave()
        }}
      >
        <Stack gap="xl">
          <ConfigureHeader
            connection={connection}
            testing={testing}
            onTest={runTestConnection}
          />

          <Tabs value={tab} onChange={setTab}>
            <Tabs.List mb="lg">
              <Tabs.Tab value="details">Details</Tabs.Tab>
              <Tabs.Tab value="credentials">Credentials</Tabs.Tab>
              <Tabs.Tab value="terminal">Terminal Access</Tabs.Tab>
              <Tabs.Tab value="native">Native Access</Tabs.Tab>
            </Tabs.List>

            <Tabs.Panel value="details" keepMounted>
              <PlaceholderTab tabName="Details" connectionName={connection.name} />
            </Tabs.Panel>

            <Tabs.Panel value="credentials" keepMounted>
              <CredentialsTab connection={connection} isAdmin={isAdmin} />
            </Tabs.Panel>

            <Tabs.Panel value="terminal" keepMounted>
              <PlaceholderTab tabName="Terminal Access" connectionName={connection.name} />
            </Tabs.Panel>

            <Tabs.Panel value="native" keepMounted>
              <PlaceholderTab tabName="Native Access" connectionName={connection.name} />
            </Tabs.Panel>
          </Tabs>

          <FormFooter
            isAdmin={isAdmin}
            saving={saving}
            deleting={deleting}
            dirty={dirty}
            onBack={() => navigate(-1)}
            onDelete={open}
            onSave={handleSave}
          />
        </Stack>
      </form>
    </>
  )
}
