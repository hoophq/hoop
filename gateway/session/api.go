package session

import (
	"net/http"
	"strconv"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service service
	}
	SessionOptionKey string
	SessionOption    struct {
		optionKey SessionOptionKey
		optionVal any
	}
	service interface {
		FindAll(*user.Context, ...*SessionOption) (*SessionList, error)
		FindOne(context *user.Context, name string) (*Session, error)
		EntityHistory(ctx *user.Context, sessionID string) ([]SessionStatusHistory, error)
		ValidateSessionID(sessionID string) error
	}
)

const (
	OptionUser       SessionOptionKey = "user"
	OptionType       SessionOptionKey = "type"
	OptionConnection SessionOptionKey = "connection"
	OptionStartDate  SessionOptionKey = "start_date"
	OptionEndDate    SessionOptionKey = "end_date"
	OptionOffset     SessionOptionKey = "offset"
	OptionLimit      SessionOptionKey = "limit"
)

var availableSessionOptions = []SessionOptionKey{
	OptionUser, OptionType, OptionConnection,
	OptionStartDate, OptionEndDate,
	OptionLimit, OptionOffset,
}

func (a *Handler) StatusHistory(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	sessionID := c.Param("session_id")
	historyList, err := a.Service.EntityHistory(context, sessionID)
	if err != nil {
		log.Errorf("failed fetching session history, err=%v", err)
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if historyList == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	c.PureJSON(http.StatusOK, historyList)
}

func (a *Handler) FindOne(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	sessionID := c.Param("session_id")
	session, err := a.Service.FindOne(context, sessionID)
	if err != nil {
		log.Errorf("failed fetching session, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching session"})
		return
	}
	if session == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.PureJSON(http.StatusOK, session)
}

func (a *Handler) FindAll(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	var options []*SessionOption
	for _, optKey := range availableSessionOptions {
		if queryOptVal, ok := c.GetQuery(string(optKey)); ok {
			var optVal any
			switch optKey {
			case OptionStartDate, OptionEndDate:
				if optTimeVal, err := time.Parse(time.RFC3339, queryOptVal); err == nil {
					optVal = optTimeVal
				}
			case OptionLimit, OptionOffset:
				if paginationOptVal, err := strconv.Atoi(queryOptVal); err == nil {
					optVal = paginationOptVal
				}
			case OptionUser:
				// don't let it use this filter if it's not an admin
				if !ctx.User.IsAdmin() {
					continue
				}
				optVal = queryOptVal
			default:
				optVal = queryOptVal
			}
			options = append(options, WithOption(optKey, optVal))
		}
	}
	if !ctx.User.IsAdmin() {
		options = append(options, WithOption(OptionUser, ctx.User.Id))
	}
	sessionList, err := a.Service.FindAll(ctx, options...)
	if err != nil {
		log.Errorf("failed listing sessions, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing sessions"})
		return
	}

	c.PureJSON(http.StatusOK, sessionList)
}
