import { Fragment, useEffect } from 'react'
import { Divider, Grid, Stack } from '@mantine/core'
import { Plus } from 'lucide-react'
import Button from '@/components/Button'
import StepHeader from './components/StepHeader'
import RoleForm from './sections/RoleForm'
import { useResourceSetupStore } from './store'

export default function RolesStep() {
  const roles = useResourceSetupStore((s) => s.roles)
  const addRole = useResourceSetupStore((s) => s.addRole)
  const loadRoleOptions = useResourceSetupStore((s) => s.loadRoleOptions)

  useEffect(() => {
    loadRoleOptions()
  }, [loadRoleOptions])

  return (
    <Stack gap="xxlAlt" pt="xl">
      <StepHeader
        title="Setup your Resource roles"
        description="Roles are the central concept in Hoop.dev that serve as secure bridges between users and your organization's resources. They enable controlled access to internal services, databases, and other resources while maintaining security and compliance."
        docs={{ href: 'https://hoop.dev/docs/', topic: 'Roles' }}
      />

      {roles.map((role, index) => (
        <Fragment key={role.id}>
          {index > 0 && <Divider />}
          <RoleForm role={role} />
        </Fragment>
      ))}

      <Grid columns={7} gutter="xl">
        <Grid.Col span={{ base: 7, sm: 5 }} offset={{ base: 0, sm: 2 }}>
          <Button variant="light" leftSection={<Plus size={16} />} onClick={addRole}>
            Add New Role
          </Button>
        </Grid.Col>
      </Grid>
    </Stack>
  )
}
