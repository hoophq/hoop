import { Fragment, useEffect, useMemo, useRef } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Box, Group } from '@mantine/core'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import FullBleed from '@/layout/FullBleed'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { setupConfigFor } from './catalog'
import { useResourceSetupStore } from './store'
import NameStep from './NameStep'
import AgentStep from './AgentStep'
import RolesStep from './RolesStep'
import SuccessStep from './SuccessStep'
import classes from './Setup.module.css'

const FORM_ID = 'resource-setup-form'

function Footer({ step }) {
  const navigate = useNavigate()
  const name = useResourceSetupStore((s) => s.name)
  const checkingName = useResourceSetupStore((s) => s.checkingName)
  const agentId = useResourceSetupStore((s) => s.agentId)
  const roles = useResourceSetupStore((s) => s.roles)
  const submitting = useResourceSetupStore((s) => s.submitting)
  const setStep = useResourceSetupStore((s) => s.setStep)
  const leaveAgentStep = useResourceSetupStore((s) => s.leaveAgentStep)
  const goToRoles = useResourceSetupStore((s) => s.goToRoles)

  const back = {
    name: () => navigate(-1),
    agent: leaveAgentStep,
    roles: () => setStep('agent'),
  }[step]

  let next
  if (step === 'name') {
    next = (
      <Button size="sm" type="submit" form={FORM_ID} disabled={!name} loading={checkingName}>
        Next: Agents
      </Button>
    )
  } else if (step === 'agent') {
    next = (
      <Button size="sm" onClick={goToRoles} disabled={!agentId}>
        Next: Resource Roles
      </Button>
    )
  } else {
    next = (
      <Button size="sm" type="submit" form={FORM_ID} disabled={roles.length === 0 || submitting}>
        {submitting ? 'Creating...' : 'Save and Finish'}
      </Button>
    )
  }

  // A new button per step: React would otherwise turn the clicked "Next" into
  // the next step's submit button while the click runs, and submit the form.
  return (
    <Group justify="flex-end" gap="lg" className={classes.footer}>
      <Button size="sm" variant="subtle" color="gray" onClick={back}>
        Back
      </Button>
      <Fragment key={step}>{next}</Fragment>
    </Group>
  )
}

export default function ResourceSetup() {
  const [searchParams] = useSearchParams()
  const catalogId = searchParams.get('type')
  const connections = useConnectionsMetadataStore((s) => s.metadata?.connections)
  const setup = useMemo(() => setupConfigFor(catalogId, connections), [catalogId, connections])
  const current = useResourceSetupStore((s) => s.setup)
  const step = useResourceSetupStore((s) => s.step)
  const start = useResourceSetupStore((s) => s.start)
  const checkName = useResourceSetupStore((s) => s.checkName)
  const submit = useResourceSetupStore((s) => s.submit)
  const scrollRef = useRef(null)

  useEffect(() => {
    start(setup)
  }, [setup, start])

  // Each step opens at its top.
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: 0 })
  }, [step])

  // The store still holds an earlier run until start() lands.
  if (!setup || current !== setup) return <PageLoader h={400} />

  const onSubmit = (event) => {
    event.preventDefault()
    if (step === 'name') checkName()
    if (step === 'roles') submit(connections)
  }

  // The agent step holds its own form (the new agent's name), and forms do not nest.
  let body
  if (step === 'success') body = <SuccessStep />
  else if (step === 'agent') body = <AgentStep />
  else {
    body = (
      <form id={FORM_ID} onSubmit={onSubmit}>
        {step === 'name' ? <NameStep /> : <RolesStep />}
      </form>
    )
  }

  return (
    <FullBleed>
      <Box ref={scrollRef} className={classes.scroll}>
        <Box className={classes.content}>{body}</Box>
        {step !== 'success' && <Footer step={step} />}
      </Box>
    </FullBleed>
  )
}
