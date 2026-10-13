//go:build hoop_credential

package codec

func init() { declared = append(declared, CapCredential) }

//export take_credential
func exportTakeCredential(conn, ptr, n uint32) uint64 {
	return give(current().takeCredential(conn, borrow(ptr, n)))
}
