import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { FEATURES, analyzerBlocked } from '../config'
import { SOURCE_GATEWAY } from '../features'
import { RULE_APIS, findRule, ruleErrorMessage, targetLanes } from '../rules'
import RuleFormModal from '../sections/RuleFormModal'
import RulePickerModal from '../sections/RulePickerModal'
import FeatureAccordions from './FeatureAccordions'

// The accordions with their Add and Edit dialogs; the sidecar is re-read after a save.
export default function FeatureRules({ sidecar, listener = null, editable = false, includeLanes = false, flush = false }) {
  const navigate = useNavigate()
  const refreshSidecar = useSidecarStore((s) => s.refreshSidecar)
  // The feature outlives `opened`, so a closing dialog keeps its copy.
  const [picker, setPicker] = useState({ feature: null, opened: false })
  const [form, setForm] = useState({ feature: null, rule: null, opened: false })

  const refresh = () =>
    refreshSidecar(sidecar.id).catch(() => {
      showSnackbar({ level: 'error', text: 'Saved, but the sidecar could not be re-read. Reload the page.' })
    })

  // A gateway rule reaching a listener through its mirror keeps its gateway
  // form: the sidecar form saves empty agent fields and would erase it.
  const editOnGateway = (key, rule) => {
    const api = RULE_APIS[key]
    findRule(api, rule.name)
      .then((found) => navigate(api.editPath(found)))
      .catch((err) => showSnackbar({ level: 'error', text: ruleErrorMessage(err) }))
  }

  // A binding names a listener, so nothing is offered without one.
  const actions =
    editable && targetLanes(sidecar, listener).length > 0
      ? {
          onAdd: (key) => setPicker({ feature: FEATURES[key], opened: true }),
          onEdit: (key, rule) =>
            rule.source === SOURCE_GATEWAY
              ? editOnGateway(key, rule)
              : setForm({ feature: FEATURES[key], rule, opened: true }),
          addBlocked: (key) => (key === 'ai-analyzer' ? analyzerBlocked(sidecar.configuration) : null),
        }
      : undefined

  return (
    <>
      <FeatureAccordions
        listener={listener}
        config={sidecar.configuration}
        boundRules={sidecar.bound_rules}
        includeLanes={includeLanes}
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
