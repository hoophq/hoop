import { useState } from 'react'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import {
  emptyListener,
  formToListener,
  hasErrors,
  listenerToForm,
  replaceListener,
  validateListener,
} from './listeners'

// Worth knowing before you add copy here: a listener change is NOT applied to a
// running sidecar. reload.go:167 compares the document with the rule sections
// nulled out and returns reloadRestart on any other difference, so topology
// waits for a restart while guardrails, masking, opa and pii hot-swap within
// one heartbeat. The UI says nothing about it on purpose — the product's
// intent is that the plane orchestrates the sidecar and restarts are not a
// user-facing idea. If that intent lands in the daemon (ADR-0014 option 2,
// rejected once), nothing here has to change.

export const saveErrorMessage = (error) =>
  error?.response?.data?.message || error?.message || 'The control plane refused the change.'

/**
 * Form state and the save for one listener, shared by both shells.
 *
 * `index` is the listener's position in the stored document, or null to add
 * one. Position rather than name, because renaming is an ordinary edit and a
 * name lookup would make it a delete plus an insert.
 *
 * Saving is a read-modify-write of the WHOLE configuration: there is no
 * per-listener endpoint. The listener the form opened with is passed back to
 * formToListener so the sections this form does not render survive.
 */
export function useListenerEditor({ sidecar, index }) {
  const listeners = sidecar?.configuration?.listeners ?? []
  const original = index === null ? null : (listeners[index] ?? null)
  const others = listeners.filter((_, i) => i !== index)

  const [form, setForm] = useState(() => (original ? listenerToForm(original) : emptyListener()))
  const [errors, setErrors] = useState({})
  const [saving, setSaving] = useState(false)
  const updateSidecar = useSidecarStore((s) => s.updateSidecar)

  // Errors are raised by a save and cleared by any edit, so a message never
  // outlives the value it was about.
  const setField = (patch) => {
    setForm((f) => ({ ...f, ...patch }))
    setErrors({})
  }

  const save = async () => {
    const found = validateListener(form, others)
    if (hasErrors(found)) {
      setErrors(found)
      return null
    }
    setSaving(true)
    const configuration = replaceListener(sidecar.configuration, index, formToListener(original, form))
    const { ok, sidecar: updated, error } = await updateSidecar(sidecar.id, configuration)
    setSaving(false)
    if (!ok) {
      showSnackbar({ level: 'error', text: 'Failed to save the listener.', description: saveErrorMessage(error) })
      return null
    }
    showSnackbar({ level: 'success', text: `Listener "${form.name.trim()}" saved.` })
    return updated
  }

  return { form, setField, errors, saving, save, isNew: index === null }
}
