package domain

type (
	Connection struct {
		Name   string `json:"name" binding:"required"`
		Type   string `json:"type" binding:"required"`
		Secret string `json:"secret" binding:"required"`
	}
)
