import SlackResult from '../SlackResult'

// /slack/organization/new: the Slack app returns here after its install.
export default function SlackNewOrganization() {
  return (
    <SlackResult
      image="/images/illustrations/keyboard.svg"
      title="Slack workspace connected"
      description="The hoop.dev app is installed in your Slack workspace."
    />
  )
}
