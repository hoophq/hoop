//go:build hoop_rewrite

package codec

func init() { declared = append(declared, CapRewrite) }

//export enable_rewrite
func exportEnableRewrite(conn uint32) {
	current().enableRewrite(conn)
}

//export rewrite
func exportRewrite(conn, ptr, n uint32) uint64 {
	return give(current().rewrite(conn, borrow(ptr, n)))
}

//export flush
func exportFlush(conn uint32) uint64 {
	return give(current().flush(conn))
}
