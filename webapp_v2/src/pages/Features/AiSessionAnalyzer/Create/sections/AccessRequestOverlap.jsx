import { Anchor, List, Text } from '@mantine/core'
import { Link } from 'react-router-dom'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import { accessRequestOverlap } from '../../helpers'

const ACCESS_REQUEST_EDIT_PATH = '/features/access-request/edit'

// The gateway checks access request rules of type command before the analyzer,
// so a role in both waits for approval whatever risk level the analyzer gives.
export default function AccessRequestOverlap({
  connectionNames,
  accessRequestRules,
  attributes,
  failed,
}) {
  if (connectionNames.length === 0) return null

  if (failed) {
    return (
      <Alert color="yellow" variant="light" icon={<TriangleAlert size={16} />} radius="md">
        Could not check whether an Access Request rule also covers these resource roles.
      </Alert>
    )
  }

  const overlap = accessRequestOverlap(connectionNames, accessRequestRules, attributes)
  if (overlap.length === 0) return null

  return (
    <Alert
      color="yellow"
      variant="light"
      icon={<TriangleAlert size={16} />}
      radius="md"
      title="An Access Request rule also covers these resource roles"
      data-testid="access-request-overlap"
    >
      <Text size="sm" mb="xs">
        Access Request rules run before the AI Session Analyzer. Commands on these roles
        can wait for approval whatever the risk level. To let the analyzer decide, remove
        the role from the Access Request rule.
      </Text>
      <List size="sm" spacing={4}>
        {overlap.map(({ connectionName, rules }) => (
          <List.Item key={connectionName}>
            <Text span size="sm" fw={600}>
              {connectionName}
            </Text>
            {': '}
            {rules.map((rule, index) => (
              <span key={rule.name}>
                {index > 0 ? ', ' : ''}
                <Anchor
                  component={Link}
                  to={`${ACCESS_REQUEST_EDIT_PATH}/${encodeURIComponent(rule.name)}`}
                  size="sm"
                >
                  {rule.name}
                </Anchor>
              </span>
            ))}
          </List.Item>
        ))}
      </List>
    </Alert>
  )
}
