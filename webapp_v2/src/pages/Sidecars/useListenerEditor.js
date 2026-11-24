import { useState } from 'react'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import {
  emptyListener,
  formToListener,
  groupSaveProblems,
  hasErrors,
  listenerToForm,
  replaceListener,
  setPath,
  validateListener,
} from './listeners'

// Worth knowing before you add copy here: a running sidecar starts an added
// listener and stops a removed one within one heartbeat, with no restart
// (sidecar/daemon/reload.go, diffTopology). An edit to a running listener's
// own fields, a rename, and an added listener on an ica sidecar wait for a
// restart; the sidecar reports that, and a listener it could not start, as
// the config state on its page. Guardrails, masking, opa and pii hot-swap.
// The form says nothing about restarts on purpose: the product's intent is
// that the plane orchestrates the sidecar.

// Prefer `problems`: `message` joins them with newlines, which a Text collapses.
export const saveErrorMessage = (error) => {
  const { problems, message } = error?.response?.data ?? {}
  if (problems?.length) return problems.join('. ')
  return message || error?.message || 'Hoop refused the change.'
}

/**
 * Form state and the save for one listener, shared by both shells.
 *
 * `index` is the listener's position in the stored document, or null to add
 * one. Position rather than name, because renaming is an ordinary edit and a
 * name lookup would make it a delete plus an insert.
 *
 * Saving is a read-modify-write of the WHOLE configuration: there is no
 * per-listener endpoint. The form state is a copy of the listener, so the
 * sections this form does not render survive.
 */
export function useListenerEditor({ sidecar, index }) {
  const listeners = sidecar?.configuration?.listeners ?? []
  const original = index === null ? null : (listeners[index] ?? null)
  const others = listeners.filter((_, i) => i !== index)

  const [form, setForm] = useState(() => (original ? listenerToForm(original) : emptyListener()))
  const [errors, setErrors] = useState({})
  const [refused, setRefused] = useState(null)
  const [saving, setSaving] = useState(false)
  const updateSidecar = useSidecarStore((s) => s.updateSidecar)

  // Errors are raised by a save and cleared by any edit, so a message never
  // outlives the value it was about.
  const setField = (path, value) => {
    setForm((f) => setPath(f, path, value))
    setErrors({})
    setRefused(null)
  }

  const save = async () => {
    const found = validateListener(form, others, original, sidecar?.configuration)
    if (hasErrors(found)) {
      setErrors(found)
      return null
    }
    setSaving(true)
    const configuration = replaceListener(sidecar.configuration, index, formToListener(form))
    const { ok, sidecar: updated, error } = await updateSidecar(sidecar.id, configuration)
    setSaving(false)
    const problems = error?.response?.data?.problems
    if (!ok && problems?.length) {
      setRefused(groupSaveProblems(problems, String(form.name ?? '').trim(), others.map((l) => l.name)))
      showSnackbar({ level: 'error', text: 'Failed to save the listener.', description: 'The sidecar would refuse it. The problems are listed at the top of the page.' })
      return null
    }
    if (!ok) {
      showSnackbar({ level: 'error', text: 'Failed to save the listener.', description: saveErrorMessage(error) })
      return null
    }
    showSnackbar({ level: 'success', text: `Listener "${String(form.name ?? '').trim()}" saved.` })
    return updated
  }

  return { form, setField, errors, refused, saving, save, isNew: index === null }
}
