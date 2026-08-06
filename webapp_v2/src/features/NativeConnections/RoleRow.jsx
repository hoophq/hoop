import { useEffect, useState } from 'react'
import { Box, Group, Image, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import Accordion from '@/components/Accordion'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { useNativeAccessStore, FLOW_STATUS } from '@/stores/useNativeAccessStore'
import { RoleRowStatus } from './RoleRowStatus'
import { RoleRowAction } from './RoleRowAction'
import { SessionPanel } from './SessionPanel'
import { DisconnectConfirmModal } from './DisconnectConfirmModal'
import { PendingReviewPanel, RequestingPanel, UnavailablePanel } from './FlowPanels'
import { deriveRowState, ROW_STATE } from './rowState'
import classes from './NativeConnections.module.css'

// A row is expandable only when the panel has something in it. The design gives
// a plain "Connect" / "Ask access" row no chevron, and a control that expands
// into nothing is a button that does nothing.
const PANEL_STATES = new Set([
  ROW_STATE.ACTIVE_BOUNDED,
  ROW_STATE.ACTIVE_PERSISTENT,
  ROW_STATE.ACCESS_REVOKED,
  ROW_STATE.PENDING_REVIEW,
])

function RowBody({ role }) {
  const status = useNativeAccessStore((s) => s.statusByName[role.name])
  const credentials = useNativeAccessStore((s) => s.credentialsByName[role.name])
  const review = useNativeAccessStore((s) => s.reviewByName[role.name])
  const error = useNativeAccessStore((s) => s.errorByName[role.name])
  const disconnect = useNativeAccessStore((s) => s.disconnect)

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
    body = (
      <PendingReviewPanel
        connectionName={role.name}
        sessionId={review?.sessionId}
        accessDurationSec={review?.accessDurationSec}
      />
    )
  } else if (credentials) {
    body = <SessionPanel credentials={credentials} onDisconnect={openConfirm} />
  } else {
    // A session exists but its secret is still loading — resumeIfActive is in
    // flight, or a fresh credential is being issued.
    body = <RequestingPanel connectionName={role.name} />
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

/** Everything left of the action button. Never contains an interactive element. */
function RowHeader({ role, state, active, iconSrc }) {
  return (
    <Group justify="space-between" wrap="nowrap" gap="md" w="100%">
      <Group gap="md" wrap="nowrap" miw={0}>
        {/* The getter always resolves to a URL, falling back to a generic icon
            when the subtype is missing from the metadata catalog. */}
        <Box className={classes.rowAvatar}>
          <Image src={iconSrc} w={20} h={20} alt="" />
        </Box>
        <Stack gap={2} miw={0}>
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
  )
}

export function RoleRow({ role, active, expanded }) {
  const getIcon = useConnectionIconGetter()
  const flowStatus = useNativeAccessStore((s) => s.statusByName[role.name])
  const review = useNativeAccessStore((s) => s.reviewByName[role.name])
  const hasCredentials = useNativeAccessStore((s) => Boolean(s.credentialsByName[role.name]))
  const resumeIfActive = useNativeAccessStore((s) => s.resumeIfActive)
  const state = deriveRowState(role, active, flowStatus, review)

  // Expanding only loads the secret for a session that already exists. It never
  // creates one — that is the action button's job.
  useEffect(() => {
    if (expanded) resumeIfActive(role.name)
  }, [expanded, role.name, resumeIfActive])

  const iconSrc = getIcon({ subtype: role.subtype, type: role.type })
  // Must agree with what RowBody can render, or a row ends up either holding
  // unreachable content or — when `expanded` was the only truthy term — losing
  // its control in the same commit that collapses it, which drops focus to
  // <body> and leaves nothing to re-open by keyboard.
  const hasPanel =
    PANEL_STATES.has(state) ||
    hasCredentials ||
    flowStatus === FLOW_STATUS.UNAVAILABLE ||
    flowStatus === FLOW_STATUS.REQUESTING

  const header = <RowHeader role={role} state={state} active={active} iconSrc={iconSrc} />

  return (
    <Accordion.Item value={role.name} className={classes.accordionItem}>
      <Box className={classes.rowHeader}>
        {hasPanel ? (
          <Accordion.Control
            classNames={{
              control: classes.rowControl,
              chevron: classes.rowChevron,
              label: classes.rowLabel,
            }}
          >
            {header}
          </Accordion.Control>
        ) : (
          <Box className={classes.rowStatic}>{header}</Box>
        )}
        <RoleRowAction role={role} state={state} />
      </Box>
      <Accordion.Panel classNames={{ content: classes.accordionPanelContent }}>
        {expanded && <RowBody role={role} />}
      </Accordion.Panel>
    </Accordion.Item>
  )
}
