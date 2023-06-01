package dcm

import (
	"fmt"
	"testing"

	"github.com/xo/dburl"
)

func TestStatement(t *testing.T) {
	// stment, err := parseCreateRoleStatementTmpl("foo", "bar", "123")
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// t.Errorf(stment)

	dsn := "postgres://_hoop_role_granter:123@192.168.15.48:5444/postgres?sslmode=disable&timeout=5s"
	u, _ := dburl.Parse(dsn)
	fmt.Println("FRAGMENT", u.RawFragment)
	fmt.Println("RAWPATH", u.RawPath)
	fmt.Println("RAWQUERY", u.RawQuery)
	fmt.Println("PATH", u.Path)
	t.Error(u.Driver)
}
