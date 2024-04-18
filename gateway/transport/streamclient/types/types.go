package streamtypes

import (
	"strings"

	"github.com/google/uuid"
)

// ID is a unique identifier used on stream clients
type ID struct {
	resourceID   string
	resourceName string
}

func (i ID) ResourceID() string   { return i.resourceID }
func (i ID) ResourceName() string { return i.resourceName }

func (i ID) String() string {
	if i.resourceID == "" {
		return uuid.NewString()
	}
	if i.resourceName != "" {
		v := uuid.NewSHA1(uuid.NameSpaceURL, []byte(
			strings.Join([]string{i.resourceID, i.resourceName}, "/"))).String()
		return v
	}
	return i.resourceID
}

// NewStreamID returns a unique identifier as uuid based on both
// attributes provided. In case resourceName is empty, the resourceID
// will be returned as is.
//
// If resourceID is empty, it will return a random uuid
func NewStreamID(resourceID, resourceName string) ID {
	return ID{resourceID: resourceID, resourceName: resourceName}
}
