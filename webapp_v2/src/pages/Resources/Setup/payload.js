import { metadataForSubtype } from './catalog'
import { secretOf } from './roleCredentials'

// The gateway ignores these on POST /resources; the CLJS wizard sends them,
// so the payload keeps them.
const ROLE_DEFAULTS = {
  access_mode_runbooks: 'enabled',
  access_mode_exec: 'enabled',
  access_mode_connect: 'enabled',
  access_schema: 'enabled',
  redact_enabled: false,
  redact_types: [],
  reviewers: [],
}

// The active protection profile's attribute rides along unless the admin
// removed its pill (with-profile-attribute in CLJS).
function roleAttributes(role, profileAttribute) {
  const withProfile = profileAttribute && !role.skipProfile ? [profileAttribute] : []
  return [...new Set([...role.attributes, ...withProfile])]
}

// POST /resources body, the same as process-payload in
// webapp/src/webapp/resources/setup/events/process_form.cljs for these types.
// The typed linux-vm arguments replace the catalog command; with none, linux-vm
// keeps ["bash"], where CLJS sends [] (Q17).
export function buildCreatePayload({ name, setup, agentId, roles, connections, profileAttribute }) {
  const catalogCommand =
    metadataForSubtype(connections, setup.subtype)?.resourceConfiguration?.command ??
    setup.command ??
    []
  return {
    name,
    type: setup.type,
    subtype: setup.subtype,
    agent_id: agentId,
    env_vars: {},
    roles: roles.map((role) => {
      const { drafts } = role.credentials.getState()
      return {
        name: role.name,
        type: setup.type,
        subtype: drafts.subtype,
        agent_id: agentId,
        secret: secretOf(role.credentials),
        command: drafts.command.length > 0 ? drafts.command : catalogCommand,
        attributes: roleAttributes(role, profileAttribute),
        ...ROLE_DEFAULTS,
      }
    }),
  }
}
