import { Checkbox, Divider, Grid, Stack, Text } from '@mantine/core'
import Accordion from '@/components/Accordion'
import NumberInput from '@/components/NumberInput'
import SegmentedControl from '@/components/SegmentedControl'
import Select from '@/components/Select'
import Switch from '@/components/Switch'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import { PROTOCOL_OPTIONS, supportsDownstreamTLS, supportsGRPCBlock, supportsHTTPBlock } from '../listeners'

// Every path in this form names a file on the SIDECAR's host. The control
// plane cannot see that filesystem and does not check them, so the hint says
// so once instead of on each field.
const PATH_HINT = 'A path on the sidecar host.'

function Section({ title, description, children }) {
  return (
    <Stack gap="sm">
      <Stack gap={2}>
        <Text fw={600} size="sm">
          {title}
        </Text>
        {description && (
          <Text size="xs" c="dimmed">
            {description}
          </Text>
        )}
      </Stack>
      {children}
    </Stack>
  )
}

/**
 * The fields of one listener, with no chrome of its own.
 *
 * Two shells render it: a modal on the sidecar's details page and a full page
 * at /sidecars/:id/listeners/*. They own the title, the buttons and the save;
 * this owns only the inputs, so the two cannot drift apart.
 *
 * `form` and `errors` come from ../listeners: `listenerToForm` builds the
 * first, `validateListener` the second.
 */
export default function ListenerForm({ form, setField, errors }) {
  const setNested = (block, patch) => setField({ [block]: { ...form[block], ...patch } })
  const unixSocket = form.network === 'unix'

  return (
    <Stack gap="lg">
      <TextInput
        label="Name"
        description="Identifies the listener in logs and in every audit event. Renaming it splits that history."
        placeholder="appdb"
        value={form.name}
        onChange={(e) => setField({ name: e.currentTarget.value })}
        error={errors.name}
        required
      />

      <Select
        label="Protocol"
        description="Selects the codec that reads this lane's traffic."
        data={PROTOCOL_OPTIONS}
        value={form.protocol || null}
        onChange={(protocol) => setField({ protocol })}
        error={errors.protocol}
        allowDeselect={false}
        required
      />

      <Stack gap="xs">
        <Text size="sm" fw={500}>
          Transport
        </Text>
        <SegmentedControl
          value={form.network}
          onChange={(network) => setField({ network })}
          data={[
            { value: 'tcp', label: 'TCP port' },
            { value: 'unix', label: 'Unix socket' },
          ]}
          w="fit-content"
        />
        <Text size="xs" c="dimmed">
          {unixSocket
            ? 'No port is opened; filesystem permissions decide who reaches the proxy.'
            : 'Reachable over the network, which is what a separate host needs.'}
        </Text>
      </Stack>

      <TextInput
        label="Listen"
        description={
          unixSocket ? `The socket the sidecar creates. ${PATH_HINT}` : 'The address outside clients connect to.'
        }
        placeholder={unixSocket ? '/run/hoop-inspect/pg.sock' : '0.0.0.0:15432'}
        value={form.listen}
        onChange={(e) => setField({ listen: e.currentTarget.value })}
        error={errors.listen}
        required
      />

      <TextInput
        label="Upstream"
        description="The real backend, as host:port."
        placeholder="appdb:5432"
        value={form.upstream}
        onChange={(e) => setField({ upstream: e.currentTarget.value })}
        error={errors.upstream}
        required
      />

      <Accordion>
        <Accordion.Item value="advanced">
          <Accordion.Control>Advanced</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="lg" pt="xs">
              <Section title="Limits">
                <Grid gutter="md">
                  <Grid.Col span={{ base: 12, sm: 6 }}>
                    <NumberInput
                      label="Max connections"
                      description="0 is unlimited."
                      min={0}
                      value={form.max_conns}
                      onChange={(v) => setField({ max_conns: Number(v) || 0 })}
                    />
                  </Grid.Col>
                  <Grid.Col span={{ base: 12, sm: 6 }}>
                    <NumberInput
                      label="Idle timeout (seconds)"
                      description="0 disables it. A short value breaks interactive sessions."
                      min={0}
                      value={form.idle_timeout_sec}
                      onChange={(v) => setField({ idle_timeout_sec: Number(v) || 0 })}
                    />
                  </Grid.Col>
                </Grid>
              </Section>

              <Divider />

              <Section
                title="Upstream TLS"
                description="Encrypts the hop to the backend. The sidecar is the TLS client there, so it still reads the traffic."
              >
                <Switch
                  label="Connect to the backend over TLS"
                  checked={form.upstream_tls_enabled}
                  onChange={(e) => setField({ upstream_tls_enabled: e.currentTarget.checked })}
                />
                {form.upstream_tls_enabled && (
                  <Stack gap="md">
                    <TextInput
                      label="CA file"
                      description={`Verifies the backend. Empty uses the host trust store. ${PATH_HINT}`}
                      placeholder="/etc/hoop-inspect/ca.pem"
                      value={form.upstream_tls.ca_file}
                      onChange={(e) => setNested('upstream_tls', { ca_file: e.currentTarget.value })}
                    />
                    <TextInput
                      label="Server name"
                      description="Overrides SNI when the dial address differs from the certificate name."
                      value={form.upstream_tls.server_name}
                      onChange={(e) => setNested('upstream_tls', { server_name: e.currentTarget.value })}
                    />
                    <Grid gutter="md">
                      <Grid.Col span={{ base: 12, sm: 6 }}>
                        <TextInput
                          label="Client certificate"
                          description={`For mTLS. ${PATH_HINT}`}
                          value={form.upstream_tls.cert_file}
                          onChange={(e) => setNested('upstream_tls', { cert_file: e.currentTarget.value })}
                        />
                      </Grid.Col>
                      <Grid.Col span={{ base: 12, sm: 6 }}>
                        <TextInput
                          label="Client key"
                          description={PATH_HINT}
                          value={form.upstream_tls.key_file}
                          onChange={(e) => setNested('upstream_tls', { key_file: e.currentTarget.value })}
                        />
                      </Grid.Col>
                    </Grid>
                    <Checkbox
                      label="Skip certificate verification"
                      description="The sidecar logs a warning at startup. Do not ship this."
                      checked={form.upstream_tls.insecure_skip_verify}
                      onChange={(e) => setNested('upstream_tls', { insecure_skip_verify: e.currentTarget.checked })}
                    />
                  </Stack>
                )}
              </Section>

              {supportsDownstreamTLS(form.protocol) && (
                <>
                  <Divider />
                  <Section
                    title="Downstream TLS"
                    description="Terminates the client's TLS on this lane. Leave both empty when something in front already does."
                  >
                    <Grid gutter="md">
                      <Grid.Col span={{ base: 12, sm: 6 }}>
                        <TextInput
                          label="Certificate"
                          description={PATH_HINT}
                          placeholder="/etc/hoop-inspect/tls.crt"
                          value={form.downstream_tls.cert_file}
                          onChange={(e) => setNested('downstream_tls', { cert_file: e.currentTarget.value })}
                          error={errors.downstream_cert_file}
                        />
                      </Grid.Col>
                      <Grid.Col span={{ base: 12, sm: 6 }}>
                        <TextInput
                          label="Key"
                          description={PATH_HINT}
                          placeholder="/etc/hoop-inspect/tls.key"
                          value={form.downstream_tls.key_file}
                          onChange={(e) => setNested('downstream_tls', { key_file: e.currentTarget.value })}
                          error={errors.downstream_key_file}
                        />
                      </Grid.Col>
                    </Grid>
                  </Section>
                </>
              )}

              {supportsHTTPBlock(form.protocol) && (
                <>
                  <Divider />
                  <Section title="HTTP" description="What this lane's codec reads out of a request.">
                    <TextInput
                      label="Identity header"
                      description="The header carrying the authenticated subject. Only trust it when nothing but your proxy can reach this listener."
                      placeholder="x-forwarded-user"
                      value={form.identity_header}
                      onChange={(e) => setField({ identity_header: e.currentTarget.value })}
                    />
                    <Switch
                      label="Capture the request body"
                      description="Needed by AI analysis rules on an HTTP lane."
                      checked={form.http.capture_body}
                      onChange={(e) => setNested('http', { capture_body: e.currentTarget.checked })}
                    />
                    <NumberInput
                      label="Max body bytes"
                      description="0 uses the codec default."
                      min={0}
                      value={form.http.max_body_bytes}
                      onChange={(v) => setNested('http', { max_body_bytes: Number(v) || 0 })}
                    />
                    <TagsInput
                      label="Headers"
                      description="Allowlist exposed to policy. Authorization, cookie, proxy-authorization and set-cookie are always refused."
                      placeholder="Add a header"
                      value={form.http.headers}
                      onChange={(headers) => setNested('http', { headers })}
                    />
                  </Section>
                </>
              )}

              {supportsGRPCBlock(form.protocol) && (
                <>
                  <Divider />
                  <Section title="gRPC" description="What this lane decodes and exposes to policy.">
                    <TagsInput
                      label="Descriptors"
                      description={`Protobuf descriptor sets. Sets merge. ${PATH_HINT}`}
                      placeholder="/etc/hoop-inspect/api.pb"
                      value={form.grpc.descriptors}
                      onChange={(descriptors) => setNested('grpc', { descriptors })}
                      error={errors.grpc_descriptors}
                    />
                    <Switch
                      label="Capture the payload"
                      description="Needed for masking, PII and AI analysis on this lane."
                      checked={form.grpc.capture_payload}
                      onChange={(e) => setNested('grpc', { capture_payload: e.currentTarget.checked })}
                    />
                    <Switch
                      label="Strict decoding"
                      description="Refuses a message the descriptors cannot explain."
                      checked={form.grpc.strict}
                      onChange={(e) => setNested('grpc', { strict: e.currentTarget.checked })}
                    />
                    <NumberInput
                      label="Max payload bytes"
                      description="0 uses the codec default."
                      min={0}
                      value={form.grpc.max_payload_bytes}
                      onChange={(v) => setNested('grpc', { max_payload_bytes: Number(v) || 0 })}
                    />
                    <TagsInput
                      label="Metadata"
                      description="Allowlist exposed to policy. The same four headers are always refused."
                      placeholder="Add a key"
                      value={form.grpc.metadata}
                      onChange={(metadata) => setNested('grpc', { metadata })}
                    />
                  </Section>
                </>
              )}
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  )
}
