// Access Control is backed by the `access_control` plugin rather than a
// dedicated resource: the group -> resource-role associations live in the
// plugin's `connections[].config` array, where each entry is a group name.
//
//   GET /plugins/access_control
//   { name: 'access_control', connections: [{ id, name, config: ['sre'] }] }
//
// PUT replaces `connections` wholesale and only reads `id` and `config` from
// each entry (the gateway re-resolves `name` on read via a join), so every
// write has to be a read-modify-write over the whole array.
export const ACCESS_CONTROL_PLUGIN = 'access_control'

// Admins and auditors bypass the plugin entirely — the connection query
// short-circuits on `is_admin_or_auditor` before it ever looks at the group
// config. Listing `admin` would offer an association that changes nothing.
const ADMIN_GROUP = 'admin'

// Protection Profiles own the attributes they create (`managed_by: "hoop"`) and
// PUT /attributes/:name rejects them with a 400. They are filtered out of both
// the picker and the diff, so this page never reads or writes one.
export function isEditableAttribute(attribute) {
  return !attribute?.managed_by
}

// group name -> the resource roles it can reach.
export function groupsWithPermissions(connections) {
  const byGroup = {}
  for (const connection of connections ?? []) {
    for (const groupName of connection.config ?? []) {
      byGroup[groupName] = byGroup[groupName] ?? []
      byGroup[groupName].push({ id: connection.id, name: connection.name })
    }
  }
  return byGroup
}

// Redundant with the gateway since EVL-217: /users/groups now unions the
// identity side with the plugin config itself. Kept as a cheap guard, because
// this page is the only way to detach a plugin-only group and losing it from
// the list would strand its permissions.
export function allGroups(userGroups, connections) {
  const names = new Set(userGroups ?? [])
  for (const connection of connections ?? []) {
    for (const groupName of connection.config ?? []) names.add(groupName)
  }
  names.delete(ADMIN_GROUP)
  return [...names].sort((a, b) => a.localeCompare(b))
}

export function groupAttributeNames(attributes, groupName) {
  return (attributes ?? [])
    .filter(isEditableAttribute)
    .filter((a) => (a.access_control_group_names ?? []).includes(groupName))
    .map((a) => a.name)
}

// A row whose config still holds group names is meaningful; one left empty is
// inert — the gateway's `config && user_groups` overlap test can never match an
// empty array, so it behaves exactly like having no row at all. Rows with a
// null config are left alone: they were never written by this page.
function isMeaningfulRow(connection) {
  return connection.config == null || connection.config.length > 0
}

// Rebuilds the plugin's connection array so `groupName` is associated with
// exactly `selectedConnectionIds` and nothing else. Connections the group does
// not reach keep whatever other groups they carry.
export function mergeGroupIntoConnections({
  pluginConnections,
  selectedConnectionIds,
  groupName,
}) {
  const selected = new Set(selectedConnectionIds ?? [])
  const existing = pluginConnections ?? []

  const updated = existing.map((connection) => {
    const isSelected = selected.has(connection.id)
    // A null config is only materialised into an array when the group is
    // actually being attached. Coalescing it to [] unconditionally would make
    // isMeaningfulRow drop the row, and since the PUT replaces the whole
    // array, a row this page never wrote would silently vanish.
    if (connection.config == null && !isSelected) return connection

    const withoutGroup = (connection.config ?? []).filter((g) => g !== groupName)
    return {
      ...connection,
      config: isSelected ? [...withoutGroup, groupName] : withoutGroup,
    }
  })

  const existingIds = new Set(existing.map((c) => c.id))
  const added = [...selected]
    .filter((id) => !existingIds.has(id))
    .map((id) => ({ id, name: '', config: [groupName] }))

  return [...updated, ...added].filter(isMeaningfulRow)
}

// Detaches a group from every resource role — used when the group itself is
// deleted, so its permissions do not linger on the plugin.
export function removeGroupFromConnections({ pluginConnections, groupName }) {
  return (pluginConnections ?? [])
    .map((connection) =>
      // Same reasoning as mergeGroupIntoConnections: a null config carries no
      // group, so there is nothing to detach — leave the row exactly as it is.
      connection.config == null
        ? connection
        : { ...connection, config: connection.config.filter((g) => g !== groupName) }
    )
    .filter(isMeaningfulRow)
}

// Which attributes have to be rewritten so that exactly `selectedNames`
// reference the group. Attributes on neither side are left untouched.
export function diffGroupAttributes({ attributes, groupName, selectedNames }) {
  const editable = (attributes ?? []).filter(isEditableAttribute)
  const selected = new Set(selectedNames ?? [])
  const current = new Set(groupAttributeNames(editable, groupName))

  return {
    added: editable.filter((a) => selected.has(a.name) && !current.has(a.name)),
    removed: editable.filter((a) => !selected.has(a.name) && current.has(a.name)),
  }
}

// PUT /attributes/:name is a full replace — every association the attribute
// should keep has to be sent back, or it is dropped.
export function attributeGroupPayload(attribute, groupName, attach) {
  const groups = new Set(attribute.access_control_group_names ?? [])
  if (attach) groups.add(groupName)
  else groups.delete(groupName)

  return {
    name: attribute.name,
    description: attribute.description ?? null,
    connection_names: attribute.connection_names ?? [],
    access_request_rule_names: attribute.access_request_rule_names ?? [],
    guardrail_rule_names: attribute.guardrail_rule_names ?? [],
    datamasking_rule_names: attribute.datamasking_rule_names ?? [],
    access_control_group_names: [...groups],
  }
}
