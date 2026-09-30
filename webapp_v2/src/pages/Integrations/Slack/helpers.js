// The Slack App is set up once both of its tokens are stored: the
// Configurations tab refuses to save either one alone, and without them the
// gateway has no app to post reviews through.
export function slackAppConfigured(plugin) {
  const envvars = plugin?.config?.envvars ?? {}
  return Boolean(envvars.SLACK_BOT_TOKEN && envvars.SLACK_APP_TOKEN)
}
