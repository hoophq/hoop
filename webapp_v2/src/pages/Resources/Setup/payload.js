import { decodeForDisplay } from '@/pages/Roles/Configure/utils/secretsCodec'
import { ENV, isStdio } from '@/pages/Roles/Configure/utils/mcpGateway'
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

// A stdio MCP Gateway role runs its typed command and sends no INSECURE; a
// remote one keeps the catalog command (the stdio carve-outs of process_form.cljs).
function mcpStdio(subtype, stagedSecrets) {
  return subtype === 'mcpproxy' && isStdio(decodeForDisplay(stagedSecrets[ENV.transport]?.value))
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
      const { drafts, stagedSecrets, mcpOAuthFlowId } = role.credentials.getState()
      const stdio = mcpStdio(setup.subtype, stagedSecrets)
      const typedCommand = setup.subtype !== 'mcpproxy' || stdio ? drafts.command : []
      const secret = secretOf(role.credentials)
      if (stdio) delete secret[ENV.insecure]
      return {
        name: role.name,
        type: setup.type,
        subtype: drafts.subtype,
        agent_id: agentId,
        secret,
        command: typedCommand.length > 0 ? typedCommand : catalogCommand,
        attributes: roleAttributes(role, profileAttribute),
        ...ROLE_DEFAULTS,
        ...(setup.subtype === 'mcpproxy' && mcpOAuthFlowId ? { mcp_oauth_flow_id: mcpOAuthFlowId } : {}),
      }
    }),
  }
}
