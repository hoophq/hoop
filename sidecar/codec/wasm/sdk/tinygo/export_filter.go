//go:build hoop_filter

package codec

func init() { declared = append(declared, CapFilter) }

//export filter
func exportFilter(conn, dir, ptr, n uint32) uint64 {
	return give(current().filter(conn, dir, borrow(ptr, n)))
}
