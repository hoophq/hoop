import { Checkbox, Divider, Grid, Stack, Text } from '@mantine/core'
import Accordion from '@/components/Accordion'
import NumberInput from '@/components/NumberInput'
import SectionRow from '@/components/SectionRow'
import SegmentedControl from '@/components/SegmentedControl'
import Select from '@/components/Select'
import Switch from '@/components/Switch'
import TagsInput from '@/components/TagsInput'
import TextInput from '@/components/TextInput'
import {
  protocolOptions,
  supportsDownstreamTLS,
  supportsGRPCBlock,
  supportsHTTPBlock,
  supportsIdentityHeader,
} from '../listeners'

// Every path in this form names a file on the SIDECAR's host. The control
// plane cannot see that filesystem and does not check them, so the hint says
// so once instead of on each field.
const PATH_HINT = 'A path on the sidecar host.'

// Inside Advanced the fields keep their own labels: the accordion is already
// one visual block, and a second 2/5 grid nested in it would indent twice.
function Block({ title, description, children }) {
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
 * The page owns the title, the pinned actions and the save; this owns only the
 * inputs. Nothing wraps them in a border: sixteen other form pages in this app
 * put their fields on the page background, and `Paper withBorder` is reserved
 * for lists and tables. The one boundary that stays is the Advanced accordion,
 * which earns it by being a different kind of area.
 *
 * `form` and `errors` come from ../listeners: `listenerToForm` builds the
 * first, `validateListener` the second.
 */
export default function ListenerForm({ form, setField, errors }) {
  const setNested = (block, patch) => setField({ [block]: { ...form[block], ...patch } })
  const unixSocket = form.network === 'unix'

  return (
    <Stack gap="xxlAlt">
      <SectionRow
        title="Identity"
        description="The name follows this listener into every log line and audit event, so renaming it splits that history. The protocol picks the codec that reads its traffic."
      >
        <Stack gap="md">
          <TextInput
            label="Name"
            placeholder="appdb"
            value={form.name}
            onChange={(e) => setField({ name: e.currentTarget.value })}
            error={errors.name}
            required
          />
          <Select
            label="Protocol"
            data={protocolOptions(form.protocol)}
            value={form.protocol || null}
            onChange={(protocol) => setField({ protocol })}
            error={errors.protocol}
            allowDeselect={false}
            required
          />
        </Stack>
      </SectionRow>

      <SectionRow
        title="Addresses"
        description="Two ends of one listener: where clients reach the sidecar, and where the sidecar reaches your resource. A unix socket opens no port, so filesystem permissions decide who can connect."
      >
        <Stack gap="md">
          <Stack gap="xs">
            {/* fw 700, not 500: the Input theme pins every real field label
                there, and this one stacks 16px above two of them. At 500 the
                section opens with what looks like a caption. */}
            <Text size="sm" fw={700}>
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
          </Stack>
          <TextInput
            label={unixSocket ? 'Listen on socket' : 'Listen on'}
            placeholder={unixSocket ? '/run/hoop-inspect/pg.sock' : '0.0.0.0:15432'}
            value={form.listen}
            onChange={(e) => setField({ listen: e.currentTarget.value })}
            error={errors.listen}
            required
          />
          <TextInput
            label="Upstream"
            placeholder="appdb:5432"
            value={form.upstream}
            onChange={(e) => setField({ upstream: e.currentTarget.value })}
            error={errors.upstream}
            required
          />
        </Stack>
      </SectionRow>

      <Accordion>
        <Accordion.Item value="advanced">
          <Accordion.Control>Advanced</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="lg" pt="xs">
              <Block title="Limits">
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
              </Block>

              {supportsIdentityHeader(form.protocol) && (
                <>
                  <Divider />
                  {/* Not inside the HTTP block: a grpc or spanner lane reads
                      this header itself (grpc.go:287), and hiding the field
                      there is what let an edit delete the key. One render site
                      rather than a copy per protocol block. */}
                  <Block title="Identity">
                    <TextInput
                      label="Identity header"
                      description="The header carrying the authenticated subject. Only trust it when nothing but your proxy can reach this listener."
                      placeholder="x-forwarded-user"
                      value={form.identity_header}
                      onChange={(e) => setField({ identity_header: e.currentTarget.value })}
                    />
                  </Block>
                </>
              )}

              <Divider />

              <Block
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
              </Block>

              {supportsDownstreamTLS(form.protocol) && (
                <>
                  <Divider />
                  <Block
                    title="Downstream TLS"
                    description="Terminates the client's TLS on this listener. Leave both empty when something in front already does."
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
                  </Block>
                </>
              )}

              {supportsHTTPBlock(form.protocol) && (
                <>
                  <Divider />
                  <Block title="HTTP" description="What this listener's codec reads out of a request.">
                    <Switch
                      label="Capture the request body"
                      description="Needed by AI analysis rules on an HTTP listener."
                      checked={form.http.capture_body}
                      onChange={(e) => setNested('http', { capture_body: e.currentTarget.checked })}
                      error={errors.http_capture_body}
                    />
                    <NumberInput
                      label="Max body bytes"
                      description="0 uses the codec default of 64 KiB."
                      min={0}
                      value={form.http.max_body_bytes}
                      onChange={(v) => setNested('http', { max_body_bytes: Number(v) || 0 })}
                      error={errors.http_max_body_bytes}
                    />
                    <TagsInput
                      label="Headers"
                      description="Allowlist exposed to policy. Authorization, cookie, proxy-authorization and set-cookie are always refused."
                      placeholder="Add a header"
                      value={form.http.headers}
                      onChange={(headers) => setNested('http', { headers })}
                      error={errors.http_headers}
                    />
                  </Block>
                </>
              )}

              {supportsGRPCBlock(form.protocol) && (
                <>
                  <Divider />
                  <Block title="gRPC" description="What this listener decodes and exposes to policy.">
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
                      description="Needed for masking, PII and AI analysis on this listener."
                      checked={form.grpc.capture_payload}
                      onChange={(e) => setNested('grpc', { capture_payload: e.currentTarget.checked })}
                      error={errors.grpc_capture_payload}
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
                      error={errors.grpc_max_payload_bytes}
                    />
                    <TagsInput
                      label="Metadata"
                      description="Allowlist exposed to policy. The same four headers are always refused."
                      placeholder="Add a key"
                      value={form.grpc.metadata}
                      onChange={(metadata) => setNested('grpc', { metadata })}
                      error={errors.grpc_metadata}
                    />
                  </Block>
                </>
              )}
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  )
}
