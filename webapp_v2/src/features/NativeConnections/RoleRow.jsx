import { useEffect, useRef, useState } from 'react'
import { Group, Image, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import Accordion from '@/components/Accordion'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { useNativeAccessStore, FLOW_STATUS } from '@/stores/useNativeAccessStore'
import { RoleRowStatus } from './RoleRowStatus'
import { SessionPanel } from './SessionPanel'
import { RequestAccessPanel } from './RequestAccessPanel'
import { DisconnectConfirmModal } from './DisconnectConfirmModal'
import { IdlePanel, PendingReviewPanel, RequestingPanel, UnavailablePanel } from './FlowPanels'
import { deriveRowState } from './rowState'
import classes from './NativeConnections.module.css'

function RowBody({ role }) {
  const status = useNativeAccessStore((s) => s.statusByName[role.name])
  const credentials = useNativeAccessStore((s) => s.credentialsByName[role.name])
  const connection = useNativeAccessStore((s) => s.connectionByName[role.name])
  const review = useNativeAccessStore((s) => s.reviewByName[role.name])
  const error = useNativeAccessStore((s) => s.errorByName[role.name])
  const disconnect = useNativeAccessStore((s) => s.disconnect)
  const startFlow = useNativeAccessStore((s) => s.startFlow)

  const [confirmOpened, { open: openConfirm, close: closeConfirm }] = useDisclosure(false)
  const [disconnecting, setDisconnecting] = useState(false)

  const confirmDisconnect = async () => {
    setDisconnecting(true)
    await disconnect(role.name, credentials?.id)
    setDisconnecting(false)
    closeConfirm()
  }

  let body
  if (status === FLOW_STATUS.UNAVAILABLE) {
    body = <UnavailablePanel message={error} />
  } else if (status === FLOW_STATUS.PENDING_REVIEW) {
    body = <PendingReviewPanel sessionId={review?.sessionId} />
  } else if (status === FLOW_STATUS.CONFIGURING) {
    body = <RequestAccessPanel connectionName={role.name} connection={connection} />
  } else if (status === FLOW_STATUS.READY && credentials) {
    body = <SessionPanel credentials={credentials} onDisconnect={openConfirm} />
  } else if (status === FLOW_STATUS.CHECKING || status === FLOW_STATUS.REQUESTING) {
    body = <RequestingPanel connectionName={role.name} />
  } else {
    // No status: either the flow has not started yet, or it was torn down by a
    // disconnect / expiry while the row stayed open.
    body = <IdlePanel onConnect={() => startFlow(role.name)} />
  }

  return (
    <>
      {body}
      <DisconnectConfirmModal
        opened={confirmOpened}
        onClose={closeConfirm}
        onConfirm={confirmDisconnect}
        connectionName={role.name}
        loading={disconnecting}
      />
    </>
  )
}

export function RoleRow({ role, active, expanded }) {
  const getIcon = useConnectionIconGetter()
  const flowStatus = useNativeAccessStore((s) => s.statusByName[role.name])
  const startFlow = useNativeAccessStore((s) => s.startFlow)
  const state = deriveRowState(role, active, flowStatus)

  // Expanding a row starts the flow — exactly once per expansion.
  //
  // The guard is a ref rather than `!flowStatus`: disconnecting (and expiring)
  // deletes the status entry, so a status-derived guard would see "no flow yet"
  // on a still-expanded row and immediately issue a fresh credential, undoing
  // the disconnect the user just confirmed.
  const startedRef = useRef(false)
  useEffect(() => {
    if (!expanded) {
      startedRef.current = false
      return
    }
    if (startedRef.current) return
    startedRef.current = true
    startFlow(role.name)
  }, [expanded, role.name, startFlow])

  const iconSrc = getIcon({ subtype: role.subtype, type: role.type })

  return (
    <Accordion.Item value={role.name}>
      <Accordion.Control classNames={{ control: classes.rowControl }}>
        <Group justify="space-between" wrap="nowrap" gap="sm">
          <Group gap="sm" wrap="nowrap" miw={0}>
            {/* The getter always resolves to a URL, falling back to a generic
                icon when the subtype is missing from the metadata catalog. */}
            <Image src={iconSrc} w={20} h={20} alt="" />
            <Stack gap={0} miw={0}>
              <Text fz="sm" fw={700} truncate>
                {role.name}
              </Text>
              <Text fz="xs" c="dimmed" truncate>
                {role.subtype || role.type}
              </Text>
            </Stack>
          </Group>
          <RoleRowStatus state={state} active={active} />
        </Group>
      </Accordion.Control>
      <Accordion.Panel>{expanded && <RowBody role={role} />}</Accordion.Panel>
    </Accordion.Item>
  )
}
