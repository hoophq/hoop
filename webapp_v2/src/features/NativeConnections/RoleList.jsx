import { Center, Skeleton, Stack, Text, ThemeIcon } from '@mantine/core'
import { CableCar, TriangleAlert } from 'lucide-react'
import Accordion from '@/components/Accordion'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import { RoleRow } from './RoleRow'
import classes from './NativeConnections.module.css'

const NATIVE_ACCESS_DOCS_URL = 'https://hoop.dev/docs/learn/connections'

// Six collapsed-row silhouettes. PageLoader is a full-screen brand loader and
// EmptyState renders a 320px illustration at 50vh — both dwarf a 600px drawer.
function LoadingRows() {
  return (
    <Stack gap="xs" px="md">
      {Array.from({ length: 6 }, (_, i) => (
        <Skeleton key={i} h={56} radius="sm" />
      ))}
    </Stack>
  )
}

function EmptyBlock({ icon, title, description, children }) {
  return (
    <Center px="md" py="xl">
      <Stack gap="sm" align="center" maw={360}>
        <ThemeIcon variant="light" color="gray" size={40} radius="xl">
          {icon}
        </ThemeIcon>
        <Text fz="sm" fw={600} ta="center">
          {title}
        </Text>
        <Text fz="xs" c="dimmed" ta="center">
          {description}
        </Text>
        {children}
      </Stack>
    </Center>
  )
}

export function RoleList({
  roles,
  activeByName,
  loading,
  error,
  query,
  expanded,
  onExpandedChange,
  onRetry,
}) {
  if (loading) return <LoadingRows />

  if (error) {
    return (
      <Stack gap="sm" px="md">
        <Alert color="red" icon={<TriangleAlert size={16} />} title="Could not load resource roles">
          {error}
        </Alert>
        <Button variant="light" size="xs" w="fit-content" onClick={onRetry}>
          Try again
        </Button>
      </Stack>
    )
  }

  if (!roles.length) {
    return query.trim() ? (
      <EmptyBlock
        icon={<CableCar size={20} aria-hidden="true" />}
        title={`No results for '${query.trim()}'`}
        description="Try a different name, type, tag or attribute."
      />
    ) : (
      <EmptyBlock
        icon={<CableCar size={20} aria-hidden="true" />}
        title="No native connections available"
        description="None of your resource roles support native access yet, or native access is turned off for them."
      >
        <DocsBtnCallOut href={NATIVE_ACCESS_DOCS_URL} text="Learn about native access" />
      </EmptyBlock>
    )
  }

  return (
    // `default` keeps every item on the same background — the expanded row is
    // marked by its separator, not by a tint. `filled` washed the whole item,
    // panel included, in grey.
    //
    // mx/mb reproduce the Figma inset: the list is a bordered card sitting 16px
    // inside the drawer, aligned with the search field above it.
    <Accordion
      variant="default"
      value={expanded}
      onChange={onExpandedChange}
      mx="md"
      mb="md"
      className={classes.accordionRoot}
    >
      {roles.map((role) => (
        <RoleRow
          key={role.name}
          role={role}
          active={activeByName[role.name]}
          expanded={expanded === role.name}
        />
      ))}
    </Accordion>
  )
}
