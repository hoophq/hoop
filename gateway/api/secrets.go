package api

import (
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/domain"
	"net/http"
)

func (a *Api) GetSecrets(c *gin.Context) {
	secrets, err := a.storage.getSecrets()
	if err != nil {
		c.Error(err)
	}
	c.JSON(http.StatusOK, secrets)
}

func (a *Api) PostSecrets(c *gin.Context) {
	var secrets domain.Secrets
	if err := c.ShouldBindJSON(&secrets); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := a.storage.persistSecrets(secrets); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

	c.JSON(http.StatusCreated, secrets)
}
