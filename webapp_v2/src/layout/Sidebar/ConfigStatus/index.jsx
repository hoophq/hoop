import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Box, Collapse, Text, UnstyledButton } from '@mantine/core'
import { useClickOutside } from '@mantine/hooks'
import { ChevronDown, ChevronUp, X } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import RingProgress from '@/components/RingProgress'
import { useUserStore } from '@/stores/useUserStore'
import { useUIStore } from '@/stores/useUIStore'
import { useConfigStatusStore } from '@/stores/useConfigStatusStore'
import { useBridgeStore } from '@/stores/useBridgeStore'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'
import { computeProgress } from './steps'
import { StepItem } from './StepItem'
import classes from './ConfigStatus.module.css'

// EVL-98: admin setup checklist.
// DEP-136: this gate is hook-free on purpose — a hidden checklist must mount no
// effects. It used to register its listeners before the early-return.
export function ConfigStatus() {
  const isAdmin = useUserStore((s) => s.isAdmin)
  const showSetupChecklist = useUserStore((s) => s.user?.show_setup_checklist)
  const userId = useUserStore((s) => s.user?.id ?? null)
  // forUserId guards a snapshot left behind by a previous login in the same tab.
  const completed = useConfigStatusStore((s) => s.completed && s.forUserId === userId)
  // Read through a selector, not useState(() => localStorage...): the gate must
  // stay effect-free, and a store read matches the three selectors above. The
  // store already dropped expired entries, so presence of the key is the answer.
  // The userId guard is for the window before /userinfo resolves: no key of the
  // map may stand in for "no user yet".
  const dismissed = useUIStore((s) => userId !== null && userId in s.configStatusDismiss)

  if (!isAdmin || !showSetupChecklist || completed || dismissed) return null
  return <ConfigStatusWidget />
}

function ConfigStatusWidget() {
  const navigate = useNavigate()
  const location = useLocation()
  const user = useUserStore((s) => s.user)
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen)
  const dismissConfigStatus = useUIStore((s) => s.dismissConfigStatus)
  const status = useConfigStatusStore((s) => s.status)
  const forUserId = useConfigStatusStore((s) => s.forUserId)
  const checks = useConfigStatusStore((s) => s.checks)
  const execConnectionName = useConfigStatusStore((s) => s.execConnectionName)
  const firstConnectionName = useConfigStatusStore((s) => s.firstConnectionName)
  const fetchStatus = useConfigStatusStore((s) => s.fetchStatus)

  const [opened, setOpened] = useState(false)
  const [activeStepId, setActiveStepId] = useState(null)
  const cardRef = useClickOutside(() => setOpened(false))

  // Returning from "Create a Resource" & friends should update the ring.
  useEffect(() => {
    fetchStatus()
  }, [location.pathname, fetchStatus])

  // Picks up work done outside this tab. Replaces the old 30s and 10s timers.
  useEffect(() => {
    const onFocus = () => fetchStatus()
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [fetchStatus])

  // Emitted by the CLJS web terminal after a successful exec, so "Run your
  // first session" ticks the moment the query runs.
  useEffect(() => {
    const onSessionExecuted = () => fetchStatus({ force: true })
    window.addEventListener('hoop:session-executed', onSessionExecuted)
    return () => window.removeEventListener('hoop:session-executed', onSessionExecuted)
  }, [fetchStatus])

  // Avoids flashing "3 steps from success" at an org that is nearly done.
  if (status !== 'ready' || forUserId !== (user?.id ?? null)) return null

  const progress = computeProgress(checks)

  const headerLabel =
    progress.stepsDone === 0
      ? `You're ${progress.totalSteps} steps from success`
      : `${progress.stepsDone} of ${progress.totalSteps} steps done`

  const toggleOpened = () => {
    if (opened) {
      setOpened(false)
      return
    }
    setActiveStepId(progress.firstIncompleteStepId)
    fetchStatus({ force: true })
    setOpened(true)
  }

  // Per user, per browser. How long it lasts depends on how much is left — see
  // useUIStore. Every destination below stays reachable from the sidebar nav,
  // so nothing becomes unreachable.
  const handleDismiss = () => {
    setOpened(false)
    dismissConfigStatus(user.id, progress.subItemsLeft)
  }

  const handleNavigate = (item) => {
    setOpened(false)
    setSidebarOpen(false) // close the mobile drawer when open; no-op on desktop

    if (item.action === 'run-first-session') {
      if (execConnectionName) {
        // The editor only reads ?role= on panel mount, so nudge an already
        // mounted one to re-read the URL.
        navigate(`/client?role=${encodeURIComponent(execConnectionName)}`)
        useBridgeStore.getState().syncPrimaryConnectionFromUrl()
      } else if (firstConnectionName) {
        // No web-terminal-capable connection — offer native access instead.
        useNativeAccessStore.getState().openAndConnect(firstConnectionName)
      } else {
        navigate('/resource-catalog')
      }
      return
    }

    navigate(item.to)
  }

  return (
    <Box ref={cardRef} className={classes.card} mb="lg">
      {/* The dismiss control is a sibling, never nested in the toggle: a button
          inside a button is invalid and swallows the inner click target. */}
      <Box className={classes.header}>
        <UnstyledButton className={classes.headerBtn} aria-expanded={opened} onClick={toggleOpened}>
          <RingProgress value={progress.percent} />
          <Text component="span" className={classes.headerLabel}>
            {headerLabel}
          </Text>
          {opened ? (
            <ChevronUp size={16} aria-hidden="true" className={classes.chevron} />
          ) : (
            <ChevronDown size={16} aria-hidden="true" className={classes.chevron} />
          )}
        </UnstyledButton>

        <ActionIcon
          variant="subtle"
          color="gray"
          size="sm"
          className={classes.closeBtn}
          aria-label="Dismiss setup checklist"
          onClick={handleDismiss}
        >
          <X size={14} aria-hidden="true" />
        </ActionIcon>
      </Box>

      <Collapse in={opened}>
        <Box className={classes.body}>
          {progress.steps.map((step) => (
            <StepItem
              key={step.id}
              step={step}
              opened={activeStepId === step.id}
              onToggle={() => setActiveStepId(activeStepId === step.id ? null : step.id)}
              onNavigate={handleNavigate}
            />
          ))}
        </Box>
      </Collapse>
    </Box>
  )
}
