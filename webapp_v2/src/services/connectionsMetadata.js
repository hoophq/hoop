// Static catalog of supported connection types, fetched at runtime from
// the same JSON that CLJS consumes (downloaded at build time from
// hoophq/documentation:store/connections.json into
// webapp/resources/public/data/connections-metadata.json). The gateway
// serves the file at /data/connections-metadata.json — same origin as
// the React bundle, not under /api — so we bypass the axios instance
// (no auth header, no 401 interceptor, no baseURL prefix).
//
// Returns the raw fetch promise; the consuming store owns status checks
// and JSON decoding. Mirrors the CLJS fetcher at
// webapp/src/webapp/events/connections.cljs:187-198.
export const connectionsMetadataService = {
  fetch: () => fetch('/data/connections-metadata.json'),
}
