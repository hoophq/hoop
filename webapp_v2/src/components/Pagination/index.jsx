import { Pagination as MantinePagination } from '@mantine/core'

/**
 * Page-based pagination control.
 *
 * Usage:
 *   <Pagination total={totalPages} value={page} onChange={setPage} />
 */
export default function Pagination(props) {
  return <MantinePagination radius="sm" {...props} />
}
