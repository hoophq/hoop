import { PostgresCredentials, PostgresConnectionUri } from './PostgresRenderer'
import { SshCredentials, SshCommand } from './SshRenderer'
import { RdpCredentials } from './RdpRenderer'
import { AwsSsmCredentials } from './AwsSsmRenderer'
import { HttpProxyCredentials, McpCredentials } from './HttpProxyRenderer'
import { ClaudeCodeCredentials } from './ClaudeCodeRenderer'
import { HTTP_PROXY_LIKE_SUBTYPES } from '../constants'
import { normalizeSubtype } from '../helpers'

/**
 * First match wins, same shape as
 * pages/Roles/Configure/sections/credentials/index.jsx.
 *
 * Matching is on the normalized subtype string alone — unlike the Roles
 * registry there is no (type, subtype) pair to disambiguate, because the
 * gateway has already collapsed the subtype by the time a credential exists.
 *
 * `tabs` is data rather than a switch in the view: SessionPanel reads it to
 * decide between a tabbed layout and a bare renderer.
 */
const RENDERER_RULES = [
  {
    name: 'postgres',
    matches: (s) => s === 'postgres',
    tabs: [
      { value: 'credentials', label: 'Credentials', render: PostgresCredentials },
      { value: 'connection-uri', label: 'Connection URI', render: PostgresConnectionUri },
    ],
  },
  {
    name: 'ssh',
    matches: (s) => s === 'ssh',
    tabs: [
      { value: 'credentials', label: 'Credentials', render: SshCredentials },
      { value: 'command', label: 'Command', render: SshCommand },
    ],
  },
  { name: 'rdp', matches: (s) => s === 'rdp', tabs: null, render: RdpCredentials },
  { name: 'aws-ssm', matches: (s) => s === 'aws-ssm', tabs: null, render: AwsSsmCredentials },
  {
    name: 'claude-code',
    matches: (s) => s === 'claude-code',
    tabs: [{ value: 'credentials', label: 'Credentials', render: ClaudeCodeCredentials }],
  },
  {
    name: 'mcp',
    matches: (s) => s === 'mcp' || s === 'mcpproxy',
    tabs: [{ value: 'credentials', label: 'Credentials', render: McpCredentials }],
  },
  {
    name: 'http-proxy',
    matches: (s) => HTTP_PROXY_LIKE_SUBTYPES.has(s),
    tabs: [{ value: 'credentials', label: 'Credentials', render: HttpProxyCredentials }],
  },
]

// Falls back to the postgres renderer for unknown subtypes, matching the CLJS
// `case` default.
export function pickCredentialRenderer(subtype) {
  const normalized = normalizeSubtype(subtype)
  return RENDERER_RULES.find((rule) => rule.matches(normalized)) ?? RENDERER_RULES[0]
}
