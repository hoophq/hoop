package webhooks

const (
	eventSessionOpenType             = "session.open"
	eventSessionCloseType            = "session.close"
	eventMSTeamsReviewCreateType     = "microsoftteams.review.create"
	EventDBRoleJobFinishedType       = "dbroles.job.finished"
	EventDBRoleJobCustomFinishedType = "dbroles.custom.job.finished"
	maxInputSize                     = 10 * 1000 // 10KB
)
