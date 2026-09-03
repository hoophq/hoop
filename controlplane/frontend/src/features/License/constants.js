// Copy and switches for the license flow. Kept out of the component files so
// react-refresh/only-export-components stays quiet and the sidecar pages can
// import the same strings.

export const RENEW_MESSAGE = 'I want to renew my hoop license'

// The gateway rejects a license whose allowed_hosts does not cover the API_URL
// hostname. The control plane and the gateway read the same license row and
// each checks its own hostname, so a license must list both.
export const HOST_HINT =
  'Ask for the license to be reissued with this hostname or a wildcard. The gateway reads the same license and checks its own hostname, so it must list both.'

// A sidecar that carries a license does not yet send it to the control plane:
// EVL-229 (token verification) is still open and the sidecar has no control
// plane client. Flip this when that path ships, so the copy below stops
// describing a feature that does not exist.
export const SIDECAR_AUTO_ACTIVATION_SHIPPED = false
export const SIDECAR_AUTO_ACTIVATION_COPY =
  'A sidecar started with a license activates the control plane when it connects.'

export const MODAL_COPY = {
  free: {
    title: 'Add your license',
    lead: 'The control plane is part of the Hoop Enterprise plan.',
    detail: 'Paste the license Hoop issued to your organization to create sidecars and rules. No license yet? Talk to sales. The open-source sidecar keeps working without a control plane.',
  },
  update: {
    title: 'Update your license',
    lead: 'Paste the new license Hoop issued to your organization.',
    detail: 'It replaces the current one and takes effect immediately.',
  },
}

export const SIDECAR_NOTICE_COPY = {
  title: 'The control plane is an Enterprise feature',
  body: 'You can open every page without a license, but creating a sidecar or a rule needs one.',
  journeys: {
    create: 'Creating a sidecar needs a license on the control plane.',
    connect: 'Connecting a sidecar needs a license on the control plane.',
  },
  footer: 'The open-source sidecar keeps working without a control plane.',
  renewal: 'Adding a sidecar needs a valid license.',
}
