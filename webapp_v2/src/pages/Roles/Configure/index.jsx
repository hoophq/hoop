import { useEffect, useState, useRef } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Stack, Text, Group } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import Modal from '@/components/Modal'
import Button from '@/components/Button'
import Tabs from '@/components/Tabs'
import { useBridgeStore } from '@/stores/useBridgeStore'
import PageLoader from '@/components/PageLoader'
import { useConfigureRoleStore } from './store'
import ConfigureHeader from './ConfigureHeader'
import FormFooter from './components/FormFooter'
import CredentialsTab from './CredentialsTab'
import DetailsTab from './DetailsTab'
import TerminalAccessTab from './TerminalAccessTab'
import NativeAccessTab from './NativeAccessTab'
import TestConnectionModal from './sections/TestConnectionModal'

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
    hasPendingChanges,
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

  const showSnackbar = useBridgeStore((s) => s.showSnackbar)

  const handleSave = async () => {
    if (formRef.current && !formRef.current.checkValidity()) {
      // Move focus to Credentials tab where required inputs live.
      setTab('credentials')
      requestAnimationFrame(() => formRef.current?.reportValidity())
      return
    }
    try {
      await save()
      showSnackbar({
        level: 'success',
        text: 'Role ' + connection.name + ' updated!',
      })
      navigate('/resources?tab=roles')
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: err?.response?.data?.message || err?.message || 'Failed to save connection.',
      })
    }
  }

  const handleDelete = async () => {
    try {
      await deleteConnection()
      close()
      showSnackbar({ level: 'success', text: 'Connection deleted.' })
      navigate('/resources?tab=roles')
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: err?.response?.data?.message || err?.message || 'Failed to delete connection.',
      })
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

  // Empty auto-placeholder rows (added by the credentials editors so
  // the section never collapses to nothing) should not trip the
  // "Unsaved changes" hint on a fresh load — the store's method
  // filters those out. Bare `useConfigureRoleStore()` above subscribes
  // to the whole slice, so re-renders happen as state changes.
  const dirty = hasPendingChanges()

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
