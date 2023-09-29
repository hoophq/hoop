package adminapi

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/runbooks/templates"
)

type runbookParametersRequest struct {
	FileContents string `json:"file_contents"`
	Name         string `json:"name"`
}

type runbookParametersResponse struct {
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata"`
}

func parseRunbookParameters(c *gin.Context) {
	var files []runbookParametersRequest
	if err := c.ShouldBindJSON(&files); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	var resp []runbookParametersResponse
	var errors []string
	for _, f := range files {
		t, err := templates.Parse(f.FileContents)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed parsing template %s, reason=%v", f.Name, err))
			continue
		}
		resp = append(resp, runbookParametersResponse{f.Name, t.Attributes()})
	}
	if len(errors) > 0 {
		msg := fmt.Sprintf("found %v error(s) listing runbook templates. %v",
			len(errors), errors)
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return
	}
	c.JSON(http.StatusOK, resp)
}

type runbookParseRequest struct {
	FileContents    string            `json:"file_contents"     binding:"required"`
	InputParameters map[string]string `json:"input_parameters"`
}

func parseRunbookTemplate(c *gin.Context) {
	var req runbookParseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	t, err := templates.Parse(req.FileContents)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	fileContents := bytes.NewBuffer([]byte{})
	if err := t.Execute(fileContents, req.InputParameters); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"file_contents":  fileContents.String(),
		"client_envvars": t.EnvVars(),
	})
}
