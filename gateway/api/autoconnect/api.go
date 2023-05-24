package apiautoconnect

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/autoconnect"
	"github.com/runopsio/hoop/gateway/transport"
)

type Handler struct {
	// store *autoconnect.Model
}

type AutoConnect struct {
	ConnectionName string `json:"connection_name"`
	Port           string `json:"port"`
	// Client map[string]string `json:"client"`
	// Time   time.Time         `json:"time"`
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

// 1. connect
// /api/autoconnect
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req AutoConnect
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	xtID, err := uuid.NewRandomFromReader(bytes.NewBufferString(ctx.UserID))
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	ac, err := autoconnect.GetEntity(ctx, xtID.String())
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	ac.RequestConnectionName = req.ConnectionName
	ac.RequestPort = req.Port
	if err := autoconnect.Put(ctx, ac); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if err := transport.DispatchSubscribe(ac); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, nil)
}
