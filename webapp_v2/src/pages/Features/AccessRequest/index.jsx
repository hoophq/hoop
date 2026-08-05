import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { ListVideo, Rotate3d } from 'lucide-react'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import Button from '@/components/Button'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import PageLoader from '@/components/PageLoader'
import ValueFilter from '@/components/ValueFilter'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { useAccessRequestStore } from './store'
import { filterRules } from './helpers'
import {
  FREE_LICENSE_MESSAGE,
  NEW_PATH,
  PROMOTION_SEEN_STORAGE_KEY,
  UPGRADE_PLAN_PATH,
  EDIT_PATH,
} from './constants'
import AccessRequestPromotion from './components/AccessRequestPromotion'
import RuleListItem from './components/RuleListItem'

const CREATE_LABEL = 'Create new Access Request rule'

export default function AccessRequest() {
  const navigate = useNavigate()

  const rules = useAccessRequestStore((s) => s.rules)
  const rulesStatus = useAccessRequestStore((s) => s.rulesStatus)
  const attributes = useAccessRequestStore((s) => s.attributes)
  const fetchRules = useAccessRequestStore((s) => s.fetchRules)
  const fetchAttributes = useAccessRequestStore((s) => s.fetchAttributes)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  // Any stored value counts as seen. Once dismissed the admin goes straight to
  // the empty state on later visits.
  const [promotionSeen, setPromotionSeen] = useState(() =>
    Boolean(localStorage.getItem(PROMOTION_SEEN_STORAGE_KEY)),
  )
  const [selectedRole, setSelectedRole] = useState(null)
  const [selectedAttribute, setSelectedAttribute] = useState(null)

  const roleFilter = usePaginatedConnections({ pageSize: 50 })

  useEffect(() => {
    fetchRules()
    fetchAttributes()
  }, [fetchRules, fetchAttributes])

  const attributeFilterValues = useMemo(
    () => [...new Set(attributes.map((a) => a.name))].sort((a, b) => a.localeCompare(b)),
    [attributes],
  )

  // Rules reference resource roles by name, so the filter matches on the
  // option's label rather than the connection id it carries.
  const filteredRules = useMemo(
    () =>
      filterRules(rules, {
        attributes,
        roleName: selectedRole?.label,
        attributeName: selectedAttribute,
      }),
    [rules, attributes, selectedRole, selectedAttribute],
  )

  const atFreeLimit = isFreeLicense && rules.length >= 1
  const loading = rulesStatus === 'idle' || rulesStatus === 'loading'
  const showLoader = useMinDelay(loading, 500)
  const activeFilterCount = (selectedRole ? 1 : 0) + (selectedAttribute ? 1 : 0)

  const goCreate = () => navigate(NEW_PATH)

  const dismissPromotion = () => {
    localStorage.setItem(PROMOTION_SEEN_STORAGE_KEY, 'true')
    setPromotionSeen(true)
    goCreate()
  }

  if (showLoader) {
    return <PageLoader h={300} />
  }

  // A failed load leaves the list empty, which would otherwise fall through to
  // the empty state and tell an admin they have no rules configured.
  if (rulesStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load access request rules." />
  }

  if (rules.length === 0 && !promotionSeen) {
    return (
      <FullBleed>
        <AccessRequestPromotion onCreate={dismissPromotion} />
      </FullBleed>
    )
  }

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>Access Request</Title>
          <Text size="md" c="dimmed">
            Create secure access request rules for your resources
          </Text>
        </Stack>
        {rules.length > 0 && (
          // At the free-plan limit the button sells the upgrade instead of
          // opening a form the gateway would reject.
          <Button onClick={atFreeLimit ? () => navigate(UPGRADE_PLAN_PATH) : goCreate}>
            {CREATE_LABEL}
          </Button>
        )}
      </Group>

      {isFreeLicense && (
        <FreeLicenseCallout
          message={FREE_LICENSE_MESSAGE}
          variant={atFreeLimit ? 'limit' : 'info'}
        />
      )}

      {rules.length === 0 ? (
        <EmptyState
          title="No Access Request rules configured in your Organization yet"
          action={{ label: CREATE_LABEL, onClick: goCreate }}
          docsUrl={docsUrl.features.reviews}
          docsLabel="Access Request documentation"
        />
      ) : (
        <>
          <Group gap="sm">
            <AsyncValueFilter
              icon={Rotate3d}
              label="Resource Role"
              placeholder="Search resource roles"
              selected={selectedRole}
              onSelect={setSelectedRole}
              onClear={() => setSelectedRole(null)}
              options={roleFilter.options}
              loading={roleFilter.loading}
              hasMore={roleFilter.hasMore}
              onLoadMore={roleFilter.loadMore}
              searchValue={roleFilter.searchValue}
              onSearchChange={roleFilter.setSearch}
              onOpen={roleFilter.ensureLoaded}
            />
            <ValueFilter
              icon={ListVideo}
              label="Attribute"
              values={attributeFilterValues}
              selected={selectedAttribute}
              onSelect={setSelectedAttribute}
              onClear={() => setSelectedAttribute(null)}
            />
          </Group>

          {filteredRules.length === 0 ? (
            <EmptyState
              compact
              title="No Access Request rules match your filters"
              description={`Try clearing the ${activeFilterCount > 1 ? 'filters' : 'filter'} above.`}
            />
          ) : (
            <Box>
              {filteredRules.map((rule, idx) => (
                <RuleListItem
                  key={rule.name}
                  rule={rule}
                  isFirst={idx === 0}
                  isLast={idx === filteredRules.length - 1}
                  onConfigure={(name) =>
                    navigate(`${EDIT_PATH}/${encodeURIComponent(name)}`)
                  }
                />
              ))}
            </Box>
          )}
        </>
      )}
    </Stack>
  )
}
