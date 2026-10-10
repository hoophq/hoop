//go:build hoop_deny

package codec

func init() { declared = append(declared, CapDeny) }

//export deny
func exportDeny(conn, dir, ptr, n uint32) uint64 {
	return give(current().deny(conn, dir, borrow(ptr, n)))
}
