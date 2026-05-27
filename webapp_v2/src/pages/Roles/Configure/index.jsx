import { useEffect, useState, useRef } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Stack, Text, Modal, Group, Button } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import Tabs from '@/components/Tabs'
import PageLoader from '@/components/PageLoader'
import { useConfigureRoleStore } from './store'
import ConfigureHeader from './components/ConfigureHeader'
import FormFooter from './components/FormFooter'
import CredentialsTab from './components/CredentialsTab'
import DetailsTab from './components/DetailsTab'
import TerminalAccessTab from './components/TerminalAccessTab'
import NativeAccessTab from './components/NativeAccessTab'
import TestConnectionModal from './components/TestConnectionModal'

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

  const {
    connection,
    loading,
    error,
    saving,
    deleting,
    testing,
    testResult,
    testModalOpen,
    testAgentStatus,
    testConnectionStatus,
    testDurationMs,
    stagedSecrets,
    drafts,
    baseline,
    loadConnection,
    loadAuxiliaryData,
    save,
    deleteConnection,
    runTestConnection,
    closeTestModal,
    reset,
  } = useConfigureRoleStore()

  const [tab, setTab] = useState('details')
  const [opened, { open, close }] = useDisclosure(false)
  const formRef = useRef(null)

  useEffect(() => {
    loadConnection(connectionName)
    loadAuxiliaryData()
    return () => reset()
  }, [connectionName, loadConnection, loadAuxiliaryData, reset])

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

  const dirty =
    Object.keys(stagedSecrets).length > 0 ||
    JSON.stringify(drafts) !== JSON.stringify(baseline)

  return (
    <>
      <DeleteConfirmationModal
        opened={opened}
        onClose={close}
        onConfirm={handleDelete}
        connectionName={connection.name}
        deleting={deleting}
      />

      <TestConnectionModal
        opened={testModalOpen}
        testing={testing}
        agentStatus={testAgentStatus}
        connectionStatus={testConnectionStatus}
        durationMs={testDurationMs}
        errorMessage={testResult?.message}
        connectionName={connection.name}
        onClose={closeTestModal}
      />

      <form
        ref={formRef}
        onSubmit={(e) => {
          e.preventDefault()
          handleSave()
        }}
      >
        <Stack gap="xl" pb={120}>
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

            <Tabs.Panel value="details" keepMounted pt="lg">
              <DetailsTab connection={connection} />
            </Tabs.Panel>

            <Tabs.Panel value="credentials" keepMounted pt="lg">
              <CredentialsTab connection={connection} />
            </Tabs.Panel>

            <Tabs.Panel value="terminal" keepMounted pt="lg">
              <TerminalAccessTab connection={connection} />
            </Tabs.Panel>

            <Tabs.Panel value="native" keepMounted pt="lg">
              <NativeAccessTab connection={connection} />
            </Tabs.Panel>
          </Tabs>

          <FormFooter
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
