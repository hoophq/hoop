import { Fragment, useEffect, useMemo, useRef } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { Box, Flex, Group, Stack } from '@mantine/core'
import { BrainCog, PackagePlus, UserRoundCheck } from 'lucide-react'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import Stepper from '@/components/Stepper'
import FullBleed from '@/layout/FullBleed'
import { LogoutButton, OnboardingLogo } from '@/layout/OnboardingLayout'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { useUserStore } from '@/stores/useUserStore'
import { hiddenByFlags, setupConfigFor } from './catalog'
import { useResourceSetupStore } from './store'
import NameStep from './NameStep'
import AgentStep from './AgentStep'
import RolesStep from './RolesStep'
import SuccessStep from './SuccessStep'
import classes from './Setup.module.css'

const FORM_ID = 'resource-setup-form'

const ONBOARDING_STEPS = [
  {
    step: 'name',
    label: 'Resource type',
    description: 'Quickly add your own services and databases or try a demo setup.',
    icon: PackagePlus,
  },
  {
    step: 'agent',
    label: 'Set up Agents',
    description: 'Establish secure communication with your infrastructure.',
    icon: BrainCog,
  },
  {
    step: 'roles',
    label: 'Resource Roles',
    description: 'Configure permissions and usage details for your Resource.',
    icon: UserRoundCheck,
  },
]

function OnboardingSteps({ step }) {
  return (
    <Box w={350} pt="xxxlAlt" px="xlAlt" visibleFrom="md" className={classes.steps}>
      <Stepper orientation="vertical" active={ONBOARDING_STEPS.findIndex((s) => s.step === step)}>
        {ONBOARDING_STEPS.map(({ step: id, label, description, icon: Icon }) => (
          <Stepper.Step key={id} label={label} description={description} icon={<Icon size={16} />} />
        ))}
      </Stepper>
    </Box>
  )
}

function Footer({ step }) {
  const navigate = useNavigate()
  const name = useResourceSetupStore((s) => s.name)
  const checkingName = useResourceSetupStore((s) => s.checkingName)
  const agentId = useResourceSetupStore((s) => s.agentId)
  const roles = useResourceSetupStore((s) => s.roles)
  const submitting = useResourceSetupStore((s) => s.submitting)
  const roleOptions = useResourceSetupStore((s) => s.roleOptions)
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
      <Button
        size="sm"
        type="submit"
        form={FORM_ID}
        disabled={roles.length === 0 || submitting || roleOptions !== 'ready'}
      >
        {submitting ? 'Creating...' : 'Save and finish'}
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

// `onboarding`: no shell, a Log out bar and the steps on the left.
export default function ResourceSetup({ onboarding = false }) {
  const [searchParams] = useSearchParams()
  const catalogId = searchParams.get('type')
  const connections = useConnectionsMetadataStore((s) => s.metadata?.connections)
  const setup = useMemo(() => setupConfigFor(catalogId, connections), [catalogId, connections])
  const current = useResourceSetupStore((s) => s.setup)
  const step = useResourceSetupStore((s) => s.step)
  const start = useResourceSetupStore((s) => s.start)
  const checkName = useResourceSetupStore((s) => s.checkName)
  const submit = useResourceSetupStore((s) => s.submit)
  const featureFlags = useUserStore((s) => s.featureFlags)
  const scrollRef = useRef(null)

  useEffect(() => {
    start(setup)
  }, [setup, start])

  // Each step opens at its top.
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: 0 })
  }, [step])

  if (hiddenByFlags(catalogId, featureFlags)) {
    return <Navigate to={onboarding ? '/onboarding/setup' : '/resource-catalog'} replace />
  }
  // The store still holds an earlier run until start() lands.
  if (!setup || current !== setup) return <PageLoader h={400} />

  const onSubmit = (event) => {
    event.preventDefault()
    if (step === 'name') checkName()
    if (step === 'roles') submit(connections)
  }

  // The agent step holds its own form (the new agent's name), and forms do not nest.
  let body
  if (step === 'success') body = <SuccessStep onboarding={onboarding} />
  else if (step === 'agent') body = <AgentStep />
  else {
    body = (
      <form id={FORM_ID} onSubmit={onSubmit}>
        {step === 'name' ? <NameStep /> : <RolesStep />}
      </form>
    )
  }

  const wizard = (
    <Box ref={scrollRef} className={classes.scroll}>
      <Box className={classes.content}>
        {onboarding && step !== 'success' && (
          <Box mb="xlAlt">
            <OnboardingLogo />
          </Box>
        )}
        {body}
      </Box>
      {step !== 'success' && <Footer step={step} />}
    </Box>
  )

  if (!onboarding) return <FullBleed>{wizard}</FullBleed>
  return (
    <Stack h="100dvh" gap={0} bg="white">
      <Group justify="flex-end" px="xl" py="md" className={classes.topBar}>
        <LogoutButton />
      </Group>
      <Flex flex={1} mih={0}>
        {step !== 'success' && <OnboardingSteps step={step} />}
        <Box flex={1} miw={0}>
          {wizard}
        </Box>
      </Flex>
    </Stack>
  )
}
