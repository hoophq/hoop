package apivalidation

import (
	"fmt"
	"regexp"
)

var (
	reResourceName, _  = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-]?[a-zA-Z0-9_]+){2,253}$`)
	errCompilingRegexp = fmt.Errorf("failed to compile regexp")
)

func ValidateResourceName(name string) error {
	if reResourceName == nil {
		return errCompilingRegexp
	}
	if !reResourceName.MatchString(name) {
		return fmt.Errorf("resource name must contain between 3 and 254 alphanumeric characters, it may include (-) or (_) characters")
	}
	return nil
}
