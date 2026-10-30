import Modal from '@/components/Modal'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import Header from './sections/Header'
import InfoModule from './sections/InfoModule'
import DetailsTabs from './sections/DetailsTabs'
import ActionBar from './sections/ActionBar'
import DetailsSkeleton from './sections/DetailsSkeleton'
import ErrorState from './sections/ErrorState'
import Insights from './insights/Insights'
import classes from './SessionDetails.module.css'

const MODAL_WIDTH = 1276

export default function SessionDetailsModal() {
  const session = useSessionDetailsStore((s) => s.session)
  const status = useSessionDetailsStore((s) => s.status)
  const error = useSessionDetailsStore((s) => s.error)
  const close = useSessionDetailsStore((s) => s.close)
  const stacked = useSessionDetailsStore((s) => s.stacked)
  const ready = status === 'ready'

  return (
    <Modal
      opened
      onClose={close}
      withCloseButton={false}
      closeOnEscape={!stacked}
      closeOnClickOutside={!stacked}
      padding={0}
      size={MODAL_WIDTH}
      aria-label="Session Details"
      classNames={{ inner: classes.inner, content: classes.content, body: classes.body }}
    >
      <div className={classes.header}>
        <Header session={session} ready={ready} onClose={close} />
      </div>
      <div className={classes.scroll}>
        {status === 'loading' && <DetailsSkeleton />}
        {status === 'error' && <ErrorState error={error} />}
        {ready && (
          <>
            <InfoModule session={session} />
            <Insights session={session} />
            <DetailsTabs key={session.id} session={session} />
          </>
        )}
      </div>
      {ready && <ActionBar session={session} className={classes.actionBar} />}
    </Modal>
  )
}
