import { useCallback, useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { usersService } from '@/services/users'
import SlackResult from '../SlackResult'

// /slack/user/new/:slackId: the Slack app links the signed-in user to its Slack id.
export default function SlackNewUser() {
  const { slackId } = useParams()
  const [status, setStatus] = useState('saving')
  const [error, setError] = useState(null)

  const save = useCallback(
    () =>
      usersService
        .updateSlackId(slackId)
        .then(() => setStatus('saved'))
        .catch((err) => {
          setError(err.response?.data?.message ?? err.message)
          setStatus('failed')
        }),
    [slackId],
  )

  useEffect(() => {
    save()
  }, [save])

  if (status === 'saving') return <PageLoader h={400} />
  if (status === 'failed') {
    return (
      <EmptyState
        title="Failed to link your user to Slack"
        description={error}
        action={{
          label: 'Try again',
          onClick: () => {
            setStatus('saving')
            save()
          },
        }}
      />
    )
  }
  return (
    <SlackResult
      image="/images/illustrations/videogame.svg"
      title="Slack user linked"
      description="Your user is linked to the hoop.dev Slack app."
    />
  )
}
