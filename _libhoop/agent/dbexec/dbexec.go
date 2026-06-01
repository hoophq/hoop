// Package dbexec is the open-source stub for the enterprise driver-based SQL
// exec surface. The real implementation (in-process pgx/mysql/mssql/oracle
// drivers) lives in the private libhoop module; this stub exposes only the
// constants the agent references so the OSS build compiles. The driver path is
// unavailable in the OSS build: libhoop.NewAdHocDBExec returns a noop proxy
// that reports the missing protocol library.
package dbexec

// Driver identifies the supported database engines for ad-hoc exec.
type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverMySQL    Driver = "mysql"
	DriverMSSQL    Driver = "mssql"
	DriverOracle   Driver = "oracledb"
)

// Option keys carry connection credentials through the libhoop opts map into
// the driver-based exec path. They reuse the names the wire proxies use so the
// agent populates one set of connection keys for either exec path.
const (
	OptKeyHost        = "hostname"
	OptKeyPort        = "port"
	OptKeyUser        = "username"
	OptKeyPassword    = "password"
	OptKeyDBName      = "dbname"
	OptKeySSLMode     = "sslmode"
	OptKeyInsecure    = "insecure"
	OptKeyServiceName = "service_name"
)
