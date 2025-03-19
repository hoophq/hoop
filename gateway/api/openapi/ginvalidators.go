package openapi

import (
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

func RegisterGinValidators() {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		// Register your custom validator
		v.RegisterValidation("db_role_job_step", validateDBRoleJobStep)
	}
}

func validateDBRoleJobStep(fl validator.FieldLevel) bool {
	stepType, ok := fl.Field().Interface().(DBRoleJobStepType)
	if !ok {
		return false
	}
	switch stepType {
	case DBRoleJobStepCreateConnections, DBRoleJobStepSendWebhook:
		return true
	}
	return false
}
