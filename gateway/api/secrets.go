package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

type (
	Secrets map[string]interface{}
)

func (a *Api) GetSecrets(c *gin.Context) {
	secrets, err := a.repository.GetSecrets()
	if err != nil {
		c.Error(err)
	}
	c.JSON(http.StatusOK, secrets)
}

func (a *Api) PostSecrets(c *gin.Context) {
	var secrets Secrets
	if err := c.ShouldBindJSON(&secrets); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := a.repository.PersistSecrets(secrets); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

	c.JSON(http.StatusCreated, secrets)
}
