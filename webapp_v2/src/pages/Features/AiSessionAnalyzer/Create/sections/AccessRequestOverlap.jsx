import { Anchor, List, Text } from '@mantine/core'
import { Link } from 'react-router-dom'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import { EDIT_PATH as ACCESS_REQUEST_EDIT_PATH } from '../../../AccessRequest/constants'
import { accessRequestOverlap } from '../../helpers'

// How the rule reaches the role, so the admin knows what to remove from it.
function matchReason({ byName, attributes }) {
  const reasons = []
  if (byName) reasons.push('lists the role')
  if (attributes.length > 0) {
    reasons.push(
      `${attributes.length > 1 ? 'attributes' : 'attribute'} ${attributes.join(', ')}`,
    )
  }
  return reasons.join(' and ')
}

// The gateway checks access request rules of type command before the analyzer,
// so a role in both waits for approval whatever risk level the analyzer gives.
// `ready` is false until both lists loaded for this page, so the warning never
// reads lists kept in the store from an earlier visit.
export default function AccessRequestOverlap({
  connectionNames,
  accessRequestRules,
  attributes,
  ready,
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

  if (!ready) return null

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
        the role, or the shared attribute, from the Access Request rule.
      </Text>
      <List size="sm" spacing={4}>
        {overlap.map(({ connectionName, rules }) => (
          <List.Item key={connectionName}>
            <Text span size="sm" fw={600}>
              {connectionName}
            </Text>
            {': '}
            {rules.map((match, index) => (
              <span key={match.rule.name}>
                {index > 0 ? ', ' : ''}
                <Anchor
                  component={Link}
                  to={`${ACCESS_REQUEST_EDIT_PATH}/${encodeURIComponent(match.rule.name)}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  size="sm"
                >
                  {match.rule.name}
                </Anchor>
                {` (${matchReason(match)})`}
              </span>
            ))}
          </List.Item>
        ))}
      </List>
    </Alert>
  )
}
