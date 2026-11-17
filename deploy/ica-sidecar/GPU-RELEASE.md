# Manual ICA GPU release

ICA GPU publication is explicitly triggered in private `hoophq/libhoop-ica`.
Dependency bumps, pull requests and normal Hoop tag releases do not start this
ICA GPU build. CPU releases continue normally.

After both implementation changes are merged, select Actions → **ICA GPU Release
(manual)** in libhoop-ica. Choose `main`, an existing stable Hoop release tag,
and `a100-cuda13-sm80-v1`, or run:

```sh
gh workflow run ica-gpu-release.yml \
  --repo hoophq/libhoop-ica --ref main \
  -f release_tag=1.301.0 \
  -f profile_id=a100-cuda13-sm80-v1
```

The private workflow resolves that tag to its exact source/engine checksum,
builds/tests or reuses an approved A100 native runtime, builds
`hoophq/hoopsidecar:<release>-ica-gpu`, publishes the native bundle and checksum,
and records GPU links/checksums in **the matching private libhoop-ica engine
release**, for example `v0.9.0`. The selected Hoop release page remains unchanged.

The large downloads remain in Hoop's artifact bucket and are linked in private
libhoop-ica release notes; GitHub release assets must each be under 2 GiB, while this native runtime
is about 4 GB. See [GitHub's limit](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases).
CPU files, common checksums, other release notes and the Latest release pointer
are preserved. Existing conflicting GPU images/immutable assets are refused.
This workflow does not create a new Hoop release or deploy a running sidecar.

Configure the private source token plus Docker Hub and artifact-bucket
credentials before the first run. Private release recording uses the built-in
workflow token; no separate release-write token is required. No GitHub App,
automatic coordination flag, or global GPU URL/SHA variables are required.
Full setup, permissions, retry behavior and limits are in the private
[manual release runbook](https://github.com/hoophq/libhoop-ica/blob/main/deploy/aks/external-gpu/runtime/MANUAL-RELEASE.md).
