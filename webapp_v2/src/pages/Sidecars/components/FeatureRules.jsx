import { useState } from 'react'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { FEATURES } from '../config'
import RuleFormModal from '../sections/RuleFormModal'
import RulePickerModal from '../sections/RulePickerModal'
import FeatureAccordions from './FeatureAccordions'

/**
 * The feature accordions with their Add and Edit, for the sidecar (no
 * `listener`) or for one lane. The dialogs write through the rule APIs, so
 * the sidecar is re-read afterwards for the bindings it now carries.
 */
export default function FeatureRules({ sidecar, listener = null, editable = false, flush = false }) {
  const refreshSidecar = useSidecarStore((s) => s.refreshSidecar)
  // The feature outlives `opened`, so a closing dialog keeps its copy.
  const [picker, setPicker] = useState({ feature: null, opened: false })
  const [form, setForm] = useState({ feature: null, rule: null, opened: false })

  const refresh = () =>
    refreshSidecar(sidecar.id).catch(() => {
      showSnackbar({ level: 'error', text: 'Saved, but the sidecar could not be re-read. Reload the page.' })
    })

  // A lane with no name has nothing a binding can point at.
  const actions =
    editable && (!listener || listener.name)
      ? {
          onAdd: (key) => setPicker({ feature: FEATURES[key], opened: true }),
          onEdit: (key, rule) => setForm({ feature: FEATURES[key], rule, opened: true }),
        }
      : undefined

  return (
    <>
      <FeatureAccordions
        listener={listener}
        config={sidecar.configuration}
        boundRules={sidecar.bound_rules}
        flush={flush}
        actions={actions}
      />
      {actions && (
        <>
          <RulePickerModal
            opened={picker.opened}
            feature={picker.feature}
            sidecar={sidecar}
            listener={listener}
            onClose={() => setPicker((p) => ({ ...p, opened: false }))}
            onCreate={() => {
              setPicker((p) => ({ ...p, opened: false }))
              setForm({ feature: picker.feature, rule: null, opened: true })
            }}
            onSaved={refresh}
          />
          <RuleFormModal
            opened={form.opened}
            feature={form.feature}
            rule={form.rule}
            sidecar={sidecar}
            listener={listener}
            onClose={() => setForm((f) => ({ ...f, opened: false }))}
            onSaved={() => {
              refresh()
              setForm((f) => ({ ...f, opened: false }))
            }}
          />
        </>
      )}
    </>
  )
}
