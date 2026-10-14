import { Group, Loader, Stack, Text, Title } from '@mantine/core'
import Badge from '@/components/Badge'
import Pagination from '@/components/Pagination'
import Table from '@/components/Table'
import { PANEL_PAGE_SIZE, pageCount } from '../helpers'

// How the gateway lets the user open the role; see ConnectionUser in the
// gateway OpenAPI spec.
function AccessPath({ user }) {
  switch (user.access_type) {
    case 'all':
      return <Text size="sm">Access control is off</Text>
    case 'admin':
      return <Badge color="indigo" variant="light">Admin</Badge>
    case 'auditor':
      return <Badge color="indigo" variant="light">Auditor</Badge>
    default:
      return (
        <Group gap="xs">
          {(user.groups ?? []).map((name) => (
            <Badge key={`group-${name}`} color="gray" variant="light">
              {name}
            </Badge>
          ))}
          {(user.attribute_groups ?? []).map((name) => (
            <Badge key={`attribute-${name}`} color="gray" variant="outline">
              {`${name} · via attribute`}
            </Badge>
          ))}
        </Group>
      )
  }
}

export default function RoleUsers({ roleName, roleUsers, onPageChange }) {
  const { items, total, page, status } = roleUsers
  const pages = pageCount(total, PANEL_PAGE_SIZE)

  let body
  if (status === 'error') {
    body = (
      <Text size="sm" c="red">
        Failed to load the users with access to this resource role.
      </Text>
    )
  } else if (status !== 'success' && items.length === 0) {
    body = <Loader size="sm" />
  } else if (total === 0) {
    body = (
      <Text size="sm" c="dimmed">
        No users have access to this resource role.
      </Text>
    )
  } else {
    body = (
      <Stack gap="sm">
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Name</Table.Th>
              <Table.Th>Email</Table.Th>
              <Table.Th>Access through</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {items.map((user) => (
              <Table.Tr key={user.id}>
                <Table.Td>
                  <Group gap="xs" wrap="nowrap">
                    <Text size="sm">{user.name || user.email}</Text>
                    {user.status !== 'active' && (
                      <Badge variant="inactive">{user.status}</Badge>
                    )}
                  </Group>
                </Table.Td>
                <Table.Td>
                  <Text size="sm" c="dimmed">
                    {user.email}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <AccessPath user={user} />
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        {pages > 1 && (
          <Pagination size="sm" total={pages} value={page} onChange={onPageChange} />
        )}
      </Stack>
    )
  }

  return (
    <Stack gap="sm">
      <Title order={4} fw={500}>
        {status === 'success'
          ? `${total} ${total === 1 ? 'user has' : 'users have'} access to ${roleName}`
          : `Users with access to ${roleName}`}
      </Title>
      {body}
    </Stack>
  )
}
