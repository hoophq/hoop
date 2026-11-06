// Releases publish hoophq/hoopsidecar:<version> and :latest (release.yml).
// A dev or PR build reports a version with no image ("unknown"): use latest.
export const sidecarImage = (version) =>
  `hoophq/hoopsidecar:${/^\d+\.\d+\.\d+$/.test(version ?? '') ? version : 'latest'}`
