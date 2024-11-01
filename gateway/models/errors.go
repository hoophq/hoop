package models

import "fmt"

var (
	ErrNotFound      = fmt.Errorf("resource not found")
	ErrAlreadyExists = fmt.Errorf("resource already exists")
)
