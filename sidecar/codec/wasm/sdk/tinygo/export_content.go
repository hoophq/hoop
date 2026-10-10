//go:build hoop_content

package codec

func init() { declared = append(declared, CapContent) }

//export content
func exportContent(conn, ptr, n uint32) uint64 {
	return give(current().content(conn, borrow(ptr, n)))
}
