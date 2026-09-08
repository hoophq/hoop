import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  SpotlightRoot,
  SpotlightSearch,
  SpotlightActionsList,
  SpotlightActionsGroup,
  SpotlightAction,
  SpotlightEmpty,
  spotlight,
} from '@mantine/spotlight'
import { Search } from 'lucide-react'
import { useUserStore } from '@/stores/useUserStore'
import { shouldHide } from '@/layout/Sidebar/helpers'
import { PALETTE } from '@/layout/Sidebar/controlPlaneNav'

// The control plane's command palette: the pages of controlPlaneNav.js, nothing
// else. Its sibling GatewayCommandPalette also searches resources, connections
// and runbooks through /search, which this product does not have. Mounted by
// ControlPlaneLayout.
export default function CommandPalette() {
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const { isAdmin, isSelfHosted, role, isFeatureFlagEnabled, isLicenseFeatureEnabled } = useUserStore()

  const needle = query.trim().toLowerCase()
  const visible = (items) =>
    items
      .filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled, role))
      .filter((i) => !needle || i.label.toLowerCase().includes(needle) || i.description?.toLowerCase().includes(needle))
  const suggestions = visible(PALETTE.suggestions)
  const quickAccess = visible(PALETTE.quickAccess)

  const go = (path) => {
    spotlight.close()
    navigate(path)
  }

  const renderGroup = (label, items) =>
    items.length > 0 && (
      <SpotlightActionsGroup label={label}>
        {items.map((item) => (
          <SpotlightAction
            key={item.id}
            label={item.label}
            description={item.description}
            leftSection={<item.icon size={16} />}
            onClick={() => go(item.path)}
          />
        ))}
      </SpotlightActionsGroup>
    )

  return (
    <SpotlightRoot
      query={query}
      onQueryChange={setQuery}
      shortcut={['mod + K']}
      scrollable
      maxHeight={400}
      clearQueryOnClose
    >
      <SpotlightSearch leftSection={<Search size={16} />} placeholder="Search pages..." />
      <SpotlightActionsList>
        {renderGroup('Suggestions', suggestions)}
        {renderGroup('Quick Access', quickAccess)}
        {suggestions.length + quickAccess.length === 0 && (
          <SpotlightEmpty>{`No results found for "${query}"`}</SpotlightEmpty>
        )}
      </SpotlightActionsList>
    </SpotlightRoot>
  )
}
