package pgusers

import (
	"fmt"
)

const LicenseFreeType = "free"

var ErrOrgAlreadyExists = fmt.Errorf("organization already exists")
