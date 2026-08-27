// Row operations shared by every editable rule table (Jira Templates,
// Guardrails). Rows are plain objects carrying a stable `id` and a `selected`
// flag; the table owns the array through a `setRows` state setter.
//
// `filterFn` exists because some tables render disjoint subsets of one shared
// rows array — select/delete must only touch the rows the table actually shows.
// Deleting the last row reseeds a blank one via `factory` so a table is never
// left with nothing to type into.
export function makeRowOps({ rows, setRows, factory, filterFn = () => true }) {
  const visible = rows.filter(filterFn)
  const allSelected = visible.length > 0 && visible.every((r) => r.selected)
  return {
    visible,
    allSelected,
    patchRow: (id, patch) =>
      setRows((current) =>
        current.map((r) => (r.id === id ? { ...r, ...patch } : r)),
      ),
    toggleSelect: (id) =>
      setRows((current) =>
        current.map((r) => (r.id === id ? { ...r, selected: !r.selected } : r)),
      ),
    toggleAll: () =>
      setRows((current) =>
        current.map((r) =>
          filterFn(r) ? { ...r, selected: !allSelected } : r,
        ),
      ),
    deleteSelected: () =>
      setRows((current) => {
        const remaining = current.filter((r) => !(filterFn(r) && r.selected))
        return remaining.length ? remaining : [factory()]
      }),
    addRow: (transform) =>
      setRows((current) => [
        ...current,
        transform ? transform(factory()) : factory(),
      ]),
  }
}
