package dbprovisioner

import "time"

type roleNameType string

const (
	rolePrefixName string = "hoop"

	readOnlyRoleName  roleNameType = "ro"
	readWriteRoleName roleNameType = "rw"
	adminRoleName     roleNameType = "ddl"

	connectionTimeoutDuration = time.Second * 15
)

var roleNames = []roleNameType{readOnlyRoleName, readWriteRoleName, adminRoleName}
