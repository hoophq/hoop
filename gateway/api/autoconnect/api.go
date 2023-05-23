package apiautoconnect

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/storagev2/autoconnect"
)

type Handler struct {
	store *autoconnect.Model
}

type AutoConnect struct {
	Status string            `json:"status"`
	Client map[string]string `json:"client"`
	Time   time.Time         `json:"time"`
}

// func New(s *storagev2.Store) *Handler { return &Handler{store: autoconnect.New(s)} }

func (h *Handler) Get(c *gin.Context) {
	// id := c.Param("id")
	// obj, err := h.store.Get(&types.UserContext{}, id)
	// if err != nil {
	// 	log.Errorf("failed fetching auto connection resource, err=%v", err)
	// 	sentry.CaptureException(err)
	// 	c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	// }
	// c.JSON(http.StatusOK, &AutoConnect{
	// 	Status: obj.Status,
	// 	Client: obj.Client,
	// })
}

func (h *Handler) Post(c *gin.Context) {
	// ctx := user.ContextUser(c)
	// var autoConn AutoConnect
	// if err := c.ShouldBindJSON(&autoConn); err != nil {
	// 	c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	// 	return
	// }
	// err := h.store.Put(&types.UserContext{}, &types.AutoConnect{
	// 	Id:    uuid.NewString(),
	// 	OrgId: ctx.Org.Id,
	// 	User:  ctx.User.Id,
	// })
	// if err != nil {
	// 	c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	// 	return
	// }
	// c.JSON(http.StatusOK, nil)
}
