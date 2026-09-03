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
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { LICENSE_FEATURE_LABELS, licenseRequiredMessage, licenseState } from '@/utils/license'
import { useAccessRequestStore } from './store'
import { filterRules } from './helpers'
import { NEW_PATH, PROMOTION_SEEN_STORAGE_KEY, EDIT_PATH } from './constants'
import AccessRequestPromotion from './components/AccessRequestPromotion'
import RuleListItem from './components/RuleListItem'

const CREATE_LABEL = 'Create new Access Request rule'

export default function AccessRequest() {
  const navigate = useNavigate()

  const rules = useAccessRequestStore((s) => s.rules)
  const rulesStatus = useAccessRequestStore((s) => s.rulesStatus)
  const fetchRules = useAccessRequestStore((s) => s.fetchRules)

  // Creating a rule needs a valid Enterprise license (hard zero on the client).
  // Editing and deleting the rules that exist stay open in every state.
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const licenseInfo = useUserStore((s) => s.licenseInfo)
  const openLicenseModal = useUIStore((s) => s.openLicenseModal)

  // Any stored value counts as seen. Once dismissed the admin goes straight to
  // the empty state on later visits.
  const [promotionSeen, setPromotionSeen] = useState(() =>
    Boolean(localStorage.getItem(PROMOTION_SEEN_STORAGE_KEY)),
  )
  const [selectedRole, setSelectedRole] = useState(null)

  const roleFilter = usePaginatedConnections({ pageSize: 50 })

  useEffect(() => {
    fetchRules()
  }, [fetchRules])

  // Rules reference resource roles by name, so the filter matches on the
  // option's label rather than the connection id it carries.
  const filteredRules = useMemo(
    () =>
      filterRules(rules, {
        attributes: [],
        roleName: selectedRole?.label,
      }),
    [rules, selectedRole],
  )

  const createBlocked = isFreeLicense
  const licenseMessage = licenseRequiredMessage(
    LICENSE_FEATURE_LABELS['access-requests'],
    licenseState(licenseInfo),
  )
  const loading = rulesStatus === 'idle' || rulesStatus === 'loading'
  const showLoader = useMinDelay(loading, 500)
  const activeFilterCount = selectedRole ? 1 : 0

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

  // While creation is blocked the promotion's primary installs a license and
  // the screen is not marked seen: the admin has not started the journey yet.
  if (rules.length === 0 && !promotionSeen) {
    return (
      <FullBleed>
        <AccessRequestPromotion
          onCreate={dismissPromotion}
          onAddLicense={createBlocked ? openLicenseModal : undefined}
        />
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
          <Button onClick={goCreate} disabled={createBlocked}>
            {CREATE_LABEL}
          </Button>
        )}
      </Group>

      {createBlocked && <FreeLicenseCallout message={licenseMessage} />}

      {rules.length === 0 ? (
        <EmptyState
          title="No Access Request rules configured in your Organization yet"
          action={
            createBlocked
              ? { label: 'Add your license', onClick: openLicenseModal }
              : { label: CREATE_LABEL, onClick: goCreate }
          }
          docsUrl={docsUrl.features.accessRequests}
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
