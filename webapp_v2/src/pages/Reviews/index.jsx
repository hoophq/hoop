import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'
import { useMinDelay } from '@/hooks/useMinDelay'
import { showSnackbar } from '@/utils/snackbar'
import { useReviewStore } from './store'
import { STATUS, STATUS_FILTERS, filterFromSearch, isSidecarReview, sortByNewest } from './helpers'
import ReviewsTable from './sections/ReviewsTable'
import ReviewModal from './sections/ReviewModal'
import RejectModal from './sections/RejectModal'

// What a settled decision did. Anything else settled is a rejection.
const SETTLED_TEXT = {
  [STATUS.APPROVED]: 'Statement released.',
  [STATUS.REVOKED]: 'Approval revoked. Running the statement again files a new review.',
}

const EMPTY = {
  waiting: {
    title: 'Nothing waiting for a decision',
    description:
      'A sidecar files a review when a listener holds a statement for approval. Approve one here or from Slack.',
  },
  settled: { title: 'No review has been decided yet' },
}

export default function Reviews() {
  const navigate = useNavigate()
  const { sessionId } = useParams()
  const [searchParams] = useSearchParams()
  const filter = filterFromSearch(searchParams)
  // Opening and closing a review keeps the list the sidebar picked.
  const search = searchParams.toString() ? `?${searchParams}` : ''

  const reviews = useReviewStore((s) => s.reviews)
  const reviewsStatus = useReviewStore((s) => s.reviewsStatus)
  const submitting = useReviewStore((s) => s.submitting)
  const fetchReviews = useReviewStore((s) => s.fetchReviews)
  const decide = useReviewStore((s) => s.decide)
  const refreshReview = useReviewStore((s) => s.refreshReview)

  const sidecars = useSidecarStore((s) => s.sidecars)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)

  const role = useUserStore((s) => s.role)
  const isAdmin = useUserStore((s) => s.isAdmin)
  const groups = useUserStore((s) => s.user?.groups)
  const adminRoleName = useUserStore((s) => s.adminRoleName)
  const approverRoleName = useUserStore((s) => s.approverRoleName)
  const user = { role, isAdmin, groups, adminRoleName, approverRoleName }

  // The review being rejected, held here rather than read from `selected` at
  // confirm time: the reject modal sits over the detail one, and a click in it
  // counts as a click outside the one below.
  const [rejecting, setRejecting] = useState(null)
  const [rejectReason, setRejectReason] = useState('')

  const closeReject = () => {
    setRejecting(null)
    setRejectReason('')
  }

  useEffect(() => {
    fetchReviews()
    fetchSidecars()
  }, [fetchReviews, fetchSidecars])

  const sidecarsById = useMemo(
    () => new Map(sidecars.map((sidecar) => [sidecar.id, sidecar])),
    [sidecars],
  )

  const visible = useMemo(() => {
    return sortByNewest(reviews.filter(STATUS_FILTERS[filter]))
  }, [reviews, filter])

  const selected = useMemo(
    () => reviews.find((review) => review.session === sessionId) ?? null,
    [reviews, sessionId],
  )

  const loading = reviewsStatus === 'idle' || reviewsStatus === 'loading'
  const showLoader = useMinDelay(loading, 500)

  const open = (review) => navigate(`/reviews/${encodeURIComponent(review.session)}${search}`)
  const close = () => navigate(`/reviews${search}`)

  const settle = async (target, status, rejectionReason) => {
    if (!target) return
    const payload = { status }
    if (rejectionReason) payload.rejection_reason = rejectionReason

    const { ok, review, error } = await decide(target.id, payload)
    if (!ok) {
      showSnackbar({
        level: 'error',
        text: 'Failed to record the decision.',
        description: error?.response?.data?.message,
      })
      // A sidecar decision fails when the sidecar or another reviewer settled
      // the review first. Re-read it, so the modal shows that state.
      if (isSidecarReview(target)) refreshReview(target.id)
      return
    }
    closeReject()
    // A review below its minimum stays pending, so the message says what changed.
    const settled = review.status !== STATUS.PENDING
    showSnackbar({
      level: 'success',
      text: settled
        ? (SETTLED_TEXT[review.status] ?? 'Statement rejected.')
        : 'Your approval was recorded. The review still needs another one.',
    })
    if (settled) close()
  }

  if (showLoader) return <PageLoader h={400} />

  if (reviewsStatus === 'error') {
    return <PageLoader error h={400} message="Failed to load reviews." />
  }

  // A session id that matches nothing would otherwise open an empty modal.
  const missing = Boolean(sessionId) && !selected

  return (
    <>
      <ReviewModal
        review={selected}
        sidecarsById={sidecarsById}
        user={user}
        opened={Boolean(selected)}
        onClose={() => !rejecting && close()}
        onApprove={() => settle(selected, STATUS.APPROVED)}
        onReject={() => setRejecting(selected)}
        onRevoke={() => settle(selected, STATUS.REVOKED)}
        submitting={submitting}
      />
      <RejectModal
        opened={rejecting != null}
        onClose={closeReject}
        onConfirm={(reason) => settle(rejecting, STATUS.REJECTED, reason)}
        loading={submitting}
        reason={rejectReason}
        onReasonChange={setRejectReason}
      />

      <Stack gap="xl">
        <Stack gap="sm">
          <Title order={1}>Reviews</Title>
          <Text size="lg" c="dimmed">
            The statements your sidecars are holding, and what was decided about them.
          </Text>
        </Stack>

        {missing && (
          <Text size="sm" c="red">
            {`No review found for session ${sessionId}.`}
          </Text>
        )}

        {visible.length === 0 ? (
          <EmptyState compact {...EMPTY[filter]} />
        ) : (
          <ReviewsTable
            reviews={visible}
            sidecarsById={sidecarsById}
            selectedId={selected?.id}
            onSelect={open}
          />
        )}
      </Stack>
    </>
  )
}
