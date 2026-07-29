import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Box, Collapse, Text, UnstyledButton } from '@mantine/core'
import { useClickOutside } from '@mantine/hooks'
import { ChevronDown, ChevronUp } from 'lucide-react'
import RingProgress from '@/components/RingProgress'
import { useUserStore } from '@/stores/useUserStore'
import { useUIStore } from '@/stores/useUIStore'
import { useConfigStatusStore } from '@/stores/useConfigStatusStore'
import { useBridgeStore } from '@/stores/useBridgeStore'
import { computeProgress, STEP_DEFS } from './steps'
import { StepItem } from './StepItem'
import classes from './ConfigStatus.module.css'

// Admin setup checklist (EVL-98). Figma behavior annotations:
// - only one step open at a time; opening another closes the rest
// - opening the widget auto-opens the first incomplete step
// - interacting with anything outside the widget collapses it
export function ConfigStatus() {
  const navigate = useNavigate()
  const location = useLocation()
  const isAdmin = useUserStore((s) => s.isAdmin)
  const user = useUserStore((s) => s.user)
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen)
  const status = useConfigStatusStore((s) => s.status)
  const forUserId = useConfigStatusStore((s) => s.forUserId)
  const checks = useConfigStatusStore((s) => s.checks)
  const execConnectionName = useConfigStatusStore((s) => s.execConnectionName)
  const firstConnectionName = useConfigStatusStore((s) => s.firstConnectionName)
  const fetchStatus = useConfigStatusStore((s) => s.fetchStatus)

  const [opened, setOpened] = useState(false)
  const [activeStepId, setActiveStepId] = useState(null)
  const cardRef = useClickOutside(() => setOpened(false))

  // Initial fetch + TTL-respecting refresh when the admin navigates (returning
  // from "Create a Resource" & friends should update the ring right away).
  useEffect(() => {
    if (isAdmin) fetchStatus()
  }, [isAdmin, location.pathname, fetchStatus])

  // Background reactivity: refresh on window focus (actions done via CLI or
  // another tab) and on a slow poll, so completing a step reflects on the
  // ring without reopening the widget. Skipped once setup is complete — the
  // widget is gone at 3/3, so a configured org never polls.
  useEffect(() => {
    if (!isAdmin) return undefined
    const tick = () => {
      const state = useConfigStatusStore.getState()
      if (state.status === 'ready' && computeProgress(state.checks).stepsDone === STEP_DEFS.length) return
      fetchStatus()
    }
    window.addEventListener('focus', tick)
    const interval = setInterval(tick, 30_000)
    return () => {
      window.removeEventListener('focus', tick)
      clearInterval(interval)
    }
  }, [isAdmin, fetchStatus])

  // Faster feedback while the admin is actually looking at the open checklist.
  useEffect(() => {
    if (!opened) return undefined
    const interval = setInterval(() => fetchStatus({ force: true }), 10_000)
    return () => clearInterval(interval)
  }, [opened, fetchStatus])

  // Never render for non-admins; stay hidden until the first snapshot
  // resolves (avoids flashing "3 steps from success" at a fully configured
  // org) and while the snapshot belongs to a previously logged-in user.
  if (!isAdmin || status !== 'ready' || forUserId !== (user?.id ?? null)) return null

  const progress = computeProgress(checks)
  if (progress.stepsDone === progress.totalSteps) return null

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

  const handleNavigate = (item) => {
    setOpened(false)
    setSidebarOpen(false) // close the mobile drawer when open; no-op on desktop

    if (item.action === 'run-first-session') {
      if (execConnectionName) {
        // The CLJS web terminal pre-selects the connection from ?role=. The
        // editor only reads it on panel mount, so also nudge an already
        // mounted/parked editor to re-read the URL.
        navigate(`/client?role=${encodeURIComponent(execConnectionName)}`)
        useBridgeStore.getState().syncPrimaryConnectionFromUrl()
      } else if (firstConnectionName) {
        // No connection supports the web terminal — offer the native client
        // flow instead (modal lives in the CLJS bundle, hence the bridge).
        navigate('/resources')
        useBridgeStore.getState().openNativeClientAccessWhenReady(firstConnectionName)
      } else {
        navigate('/resource-catalog')
      }
      return
    }

    navigate(item.to)
  }

  return (
    <Box ref={cardRef} className={classes.card} mb="lg">
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
