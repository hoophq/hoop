import { SpotlightAction, SpotlightActionsGroup, SpotlightEmpty } from '@mantine/spotlight'
import { useUserStore } from '@/stores/useUserStore'
import { shouldHide } from '@/layout/Sidebar/helpers'
import { SUGGESTION_ITEMS, QUICK_ACCESS_ITEMS } from './constants'

// Navigation only. In webapp_v2 this page also searched resources, roles and runbooks
// through GET /search and offered per-connection actions (web terminal, native client).
// None of that has a home in the control plane: resources are derived from sidecars
// rather than searched by name, and nobody reaches a resource through this app —
// it is an admin surface. Search over sidecars and reviews needs an API that does not
// exist yet; when it does, it comes back here.

const matches = (item, q) =>
  !q ||
  item.label.toLowerCase().includes(q) ||
  (item.description || '').toLowerCase().includes(q)

export default function MainPage({ query, onNavigate }) {
  const { role, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled } = useUserStore()
  const q = query.trim().toLowerCase()

  const visible = (items) =>
    items
      .filter((i) => !shouldHide(i, role, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled))
      .filter((i) => matches(i, q))

  const suggestions = visible(SUGGESTION_ITEMS)
  const quickAccess = visible(QUICK_ACCESS_ITEMS)

  if (!suggestions.length && !quickAccess.length) {
    return <SpotlightEmpty>{`No results found for "${query}"`}</SpotlightEmpty>
  }

  const group = (label, items) =>
    items.length > 0 && (
      <SpotlightActionsGroup label={label}>
        {items.map((item) => (
          <SpotlightAction
            key={item.id}
            label={item.label}
            description={item.description}
            leftSection={<item.icon size={16} />}
            onClick={() => onNavigate(item.path)}
          />
        ))}
      </SpotlightActionsGroup>
    )

  return (
    <>
      {group('Suggestions', suggestions)}
      {group('Quick Access', quickAccess)}
    </>
  )
}
