import { useEffect, useState } from 'react'
import { RotateCw } from 'lucide-react'
import Button from '@/components/Button'
import JiraPromptGate from '@/features/JiraPromptGate'
import { useJiraPromptStore } from '@/stores/useJiraPromptStore'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { showSnackbar } from '@/utils/snackbar'

// Re-run, after the resource's Jira template prompts when it has them (A07).
export default function RerunAction({ session, buttonProps }) {
  const rerun = useSessionDetailsStore((s) => s.rerun)
  const rerunning = useSessionDetailsStore((s) => s.rerunning)
  const setStacked = useSessionDetailsStore((s) => s.setStacked)
  const [checking, setChecking] = useState(false)
  const [prompting, setPrompting] = useState(false)

  useEffect(() => {
    setStacked(prompting)
    return () => setStacked(false)
  }, [prompting, setStacked])

  const start = async () => {
    setChecking(true)
    try {
      const result = await useJiraPromptStore.getState().load(session.connection)
      if (!result) return
      if (result.needed) setPrompting(true)
      else await rerun(result.fields)
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: 'Failed to load the Jira template',
        description: err?.response?.data?.message || err?.message,
      })
    } finally {
      setChecking(false)
    }
  }

  return (
    <>
      <Button {...buttonProps} leftSection={<RotateCw size={16} />} loading={checking || rerunning} onClick={start}>
        Re-run
      </Button>
      <JiraPromptGate
        opened={prompting}
        zIndex={400}
        onCancel={() => setPrompting(false)}
        onSubmit={(fields) => {
          setPrompting(false)
          rerun(fields)
        }}
      />
    </>
  )
}
