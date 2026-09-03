import { Anchor } from '@mantine/core'
import Button from '@/components/Button'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { LICENSE_STATE, licenseState } from '@/utils/license'

/**
 * The one trigger for the license modal, as a Button (default) or an Anchor
 * (`variant="anchor"`). Admin only: PUT /orgs/license is AdminOnly, so a
 * non-admin gets nothing to click. The label follows the license state:
 * "Add license" on the free tier, "Update license" once a document exists.
 *
 * Usage:
 *   <AddLicenseCta />
 *   <AddLicenseCta variant="anchor" c="red" label="Update license" />
 */
export default function AddLicenseCta({ variant = 'button', label, ...props }) {
  const isAdmin = useUserStore((state) => state.isAdmin)
  const licenseInfo = useUserStore((state) => state.licenseInfo)
  const openLicenseModal = useUIStore((state) => state.openLicenseModal)

  if (!isAdmin) return null

  const state = licenseState(licenseInfo)
  const hasDocument = state !== LICENSE_STATE.FREE && state !== LICENSE_STATE.UNKNOWN
  const text = label ?? (hasDocument ? 'Update license' : 'Add license')

  if (variant === 'anchor') {
    return (
      <Anchor component="button" type="button" fw={500} size="sm" onClick={openLicenseModal} {...props}>
        {text}
      </Anchor>
    )
  }

  return (
    <Button onClick={openLicenseModal} {...props}>
      {text}
    </Button>
  )
}
