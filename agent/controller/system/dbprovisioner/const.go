package dbprovisioner

type roleNameType string

const (
	rolePrefixName string = "hoop"

	readOnlyRoleName  roleNameType = "ro"
	readWriteRoleName roleNameType = "rw"
	adminRoleName     roleNameType = "ddl"
)

var roleNames = []roleNameType{readOnlyRoleName, readWriteRoleName, adminRoleName}
