package session

import (
	"log"
	"net/http"
	"strconv"
	"time"

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

func (a *Handler) FindOne(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	sessionID := c.Param("session_id")
	session, err := a.Service.FindOne(context, sessionID)
	if err != nil {
		log.Printf("failed obtaining session, err=%v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if session == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.PureJSON(http.StatusOK, session)
}

func (a *Handler) FindAll(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

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
			default:
				optVal = queryOptVal
			}
			options = append(options, WithOption(optKey, optVal))
		}
	}
	sessionList, err := a.Service.FindAll(context, options...)
	if err != nil {
		log.Printf("failed listing sessions, err=%v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.PureJSON(http.StatusOK, sessionList)
}
