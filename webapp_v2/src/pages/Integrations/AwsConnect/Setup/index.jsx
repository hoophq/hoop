import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Image, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import ConfirmDialog from '@/components/ConfirmDialog'
import FormFooter, { FORM_FOOTER_CLEARANCE } from '@/components/FormFooter'
import PageLoader from '@/components/PageLoader'
import Stepper from '@/components/Stepper'
import { useAgentStore } from '@/stores/useAgentStore'
import { useUserStore } from '@/stores/useUserStore'
import { useAwsJobsStore } from '../store'
import { JOBS_PATH, limitReached } from '../utils'
import AccountsStep from './sections/AccountsStep'
import CredentialsStep from './sections/CredentialsStep'
import ResourcesStep from './sections/ResourcesStep'
import ReviewStep from './sections/ReviewStep'
import RootPasswordsModal from './sections/RootPasswordsModal'
import StatusStep from './sections/StatusStep'
import { useAwsConnectSetupStore } from './store'
import { STEPS } from './wizard'

const BACK_PATH = { create: JOBS_PATH, onboarding: '/onboarding/resource-providers' }
const MISSING_AGENT = 'Please assign an agent to all selected resources before proceeding.'

const BODY = {
  credentials: CredentialsStep,
  accounts: AccountsStep,
  resources: ResourcesStep,
  review: ReviewStep,
  status: StatusStep,
}

/**
 * The AWS discovery wizard. `create` renders in the app shell
 * (/integrations/aws-connect/setup), `onboarding` without it
 * (/onboarding/aws-connect). Every visit starts clean.
 */
export default function AwsConnectSetup({ mode = 'create' }) {
  const navigate = useNavigate()
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const fetchAgents = useAgentStore((s) => s.fetchAgents)
  const jobsLoaded = useAwsJobsStore((s) => s.loaded)
  const jobs = useAwsJobsStore((s) => s.jobs)
  const fetchJobs = useAwsJobsStore((s) => s.fetchJobs)
  const store = useAwsConnectSetupStore()
  const [started, setStarted] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [validationError, setValidationError] = useState(null)
  const mounted = useRef(false)

  // Once per visit, StrictMode included: one GET /agents (Q33).
  useEffect(() => {
    if (mounted.current) return
    mounted.current = true
    useAwsConnectSetupStore.getState().reset()
    fetchAgents()
    fetchJobs().finally(() => setStarted(true))
  }, [fetchAgents, fetchJobs])

  const limit = started && jobsLoaded && isFreeLicense && limitReached(jobs)
  useEffect(() => {
    if (limit) navigate(JOBS_PATH, { replace: true })
  }, [limit, navigate])

  const { step, busy, selectedAccounts, selectedResources, agentAssignments, rootPasswords } = store

  const handleNext = () => {
    if (step === 'review') {
      if (selectedResources.some((id) => !agentAssignments[id])) {
        setValidationError(MISSING_AGENT)
        return
      }
      setValidationError(null)
      setConfirming(true)
    }
  }

  const footer = {
    credentials: { back: 'Back', next: 'Next: Accounts', onNext: store.saveCredentials },
    accounts: {
      back: 'Back to Credentials',
      onBack: () => store.update({ step: 'credentials' }),
      next: 'Next: Resources',
      disabled: selectedAccounts.length === 0,
      onNext: store.describeInstances,
    },
    resources: {
      back: 'Back to Accounts',
      onBack: () => store.update({ step: 'accounts' }),
      next: 'Next: Review',
      disabled: selectedResources.length === 0,
      onNext: () => store.update({ step: 'review' }),
    },
    review: {
      back: 'Back to Resources',
      onBack: () => store.update({ step: 'resources' }),
      next: 'Confirm and Reset Password',
      onNext: handleNext,
    },
    status: { next: 'Go to AWS Connect', onNext: () => navigate(JOBS_PATH) },
  }[step]

  const Body = BODY[step]

  const content = (
    <Box pb={FORM_FOOTER_CLEARANCE}>
      <Stack maw={1000} mx="auto" gap="xl" align="center">
        <Image src="/images/hoop-branding/PNG/hoop-symbol_black@4x.png" alt="Hoop" w={64} h={64} fit="contain" />
        <Stack gap="xs" align="center">
          <Title order={1}>AWS Connect</Title>
          <Text size="md">Follow the steps to setup your AWS resources.</Text>
        </Stack>
        <Stepper active={STEPS.findIndex((s) => s.id === step)}>
          {STEPS.map((s) => (
            <Stepper.Step key={s.id} label={s.label} />
          ))}
        </Stepper>
        {busy ? (
          <PageLoader h={400} message={busy} />
        ) : (
          <Body validationError={validationError} onAgentChange={() => setValidationError(null)} />
        )}
      </Stack>

      <FormFooter
        left={
          footer.back && (
            <Button
              variant="light"
              color="gray"
              disabled={!!busy}
              onClick={footer.onBack ?? (() => navigate(BACK_PATH[mode]))}
            >
              {footer.back}
            </Button>
          )
        }
      >
        <Button disabled={!!busy || footer.disabled} onClick={footer.onNext}>
          {footer.next}
        </Button>
      </FormFooter>

      <ConfirmDialog
        opened={confirming}
        title="Confirm finishing setup?"
        confirmLabel="Confirm and Reset Password"
        danger
        onClose={() => setConfirming(false)}
        onConfirm={() => {
          setConfirming(false)
          store.resetRootPasswords()
        }}
      >
        <Text size="sm">
          Completing this setup will automatically reset the root passwords for your selected database resources. These
          new credentials will be securely stored and accessible only through Hoop.dev.
        </Text>
        <Text size="sm">Do you want to proceed and confirm finishing this setup?</Text>
      </ConfirmDialog>

      {rootPasswords && <RootPasswordsModal credentials={rootPasswords} onFinish={store.startJobs} />}
    </Box>
  )

  if (mode === 'onboarding') {
    return (
      <Box mih="100vh" bg="white" p="xl">
        {content}
      </Box>
    )
  }
  return content
}
