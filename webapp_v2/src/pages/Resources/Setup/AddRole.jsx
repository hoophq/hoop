import { useEffect, useRef } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Box, Group, Stack } from '@mantine/core'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { useResourceSetupStore } from './store'
import RolesStep from './RolesStep'
import SuccessStep from './SuccessStep'
import classes from './Setup.module.css'

const FORM_ID = 'resource-add-role-form'

function Footer() {
  const navigate = useNavigate()
  const name = useResourceSetupStore((s) => s.name)
  const roles = useResourceSetupStore((s) => s.roles)
  const submitting = useResourceSetupStore((s) => s.submitting)
  const roleOptions = useResourceSetupStore((s) => s.roleOptions)

  return (
    <Group justify="flex-end" gap="lg" className={classes.footer}>
      <Button
        size="sm"
        variant="subtle"
        color="gray"
        onClick={() => navigate('/resources/configure/' + encodeURIComponent(name))}
      >
        Back
      </Button>
      <Button
        size="sm"
        type="submit"
        form={FORM_ID}
        disabled={roles.length === 0 || submitting || roleOptions !== 'ready'}
      >
        {submitting ? 'Creating...' : 'Save and Finish'}
      </Button>
    </Group>
  )
}

// /resources/:resourceName/add-role for the families React creates. The route
// switch (modes/ByAddRoleSetup) reads the resource into `target`.
export default function ResourceAddRole() {
  const navigate = useNavigate()
  const { resourceName } = useParams()
  const target = useResourceSetupStore((s) => s.target)
  const resource = useResourceSetupStore((s) => s.resource)
  const step = useResourceSetupStore((s) => s.step)
  const startAddRole = useResourceSetupStore((s) => s.startAddRole)
  const loadTarget = useResourceSetupStore((s) => s.loadTarget)
  const addRoles = useResourceSetupStore((s) => s.addRoles)
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const metadataError = useConnectionsMetadataStore((s) => s.error)
  const loadMetadata = useConnectionsMetadataStore((s) => s.load)
  const scrollRef = useRef(null)

  useEffect(() => {
    loadMetadata()
  }, [loadMetadata])

  useEffect(() => {
    if (target.resource) startAddRole(target.resource)
  }, [target.resource, startAddRole])

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: 0 })
  }, [step])

  if (target.name !== resourceName || target.status === 'loading') return <PageLoader h={400} />
  if (target.status === 'not-found') {
    return (
      <EmptyState
        title="Resource not found"
        description={'No resource is named ' + target.name + '.'}
        action={{ label: 'Go to Resources', onClick: () => navigate('/resources?tab=resources') }}
      />
    )
  }
  if (target.status === 'error') {
    return (
      <Stack h={400} align="center" justify="center" gap="md">
        <PageLoader h="auto" error message="Could not load the resource." description={target.error} />
        <Button variant="default" onClick={() => loadTarget(target.name)}>
          Try again
        </Button>
      </Stack>
    )
  }
  if (!metadata) {
    if (!metadataError) return <PageLoader h={400} />
    return (
      <EmptyState
        title="Could not load the resource catalog"
        description={metadataError}
        action={{ label: 'Try again', onClick: loadMetadata }}
      />
    )
  }
  // The store still holds an earlier run until startAddRole() lands.
  if (!target.resource || resource !== target.resource) return <PageLoader h={400} />

  const onSubmit = (event) => {
    event.preventDefault()
    addRoles(metadata.connections)
  }

  return (
    <FullBleed>
      <Box ref={scrollRef} className={classes.scroll}>
        <Box className={classes.content}>
          {step === 'success' ? (
            <SuccessStep />
          ) : (
            <form id={FORM_ID} onSubmit={onSubmit}>
              <RolesStep />
            </form>
          )}
        </Box>
        {step !== 'success' && <Footer />}
      </Box>
    </FullBleed>
  )
}
