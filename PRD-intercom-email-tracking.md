# PRD: Email-Addressable Analytics for OSS Users

**Status:** Draft
**Owner:** TBD
**Date:** 2026-04-20

## Summary

Hoop's gateway currently SHA-256 hashes user IDs and strips all PII (email, name) from every Segment `Identify`/`Track` call before forwarding to Mixpanel, PostHog, and Intercom. As a result, Intercom has no addressable contact for new OSS users — we cannot email them for onboarding, product updates, or support outreach.

This PRD proposes inverting the default: **analytics-opted-in users send identifiable traits (email, name) so Intercom can reach them**, while users who explicitly opt into anonymous tracking retain the current hashed behavior.

## Problem

**Root cause: the default "identified" analytics path is actually anonymizing users.** There is no explicit mode today — every user flows through the same `Identify()` in `gateway/analytics/segment.go:55`, which SHA-256 hashes the user ID and omits email/name entirely. What should be the identified path has been treated as if it were the anonymous path, so users who never opted out of tracking still land in Intercom/Mixpanel/PostHog without an addressable identity.

Downstream effects:
- New OSS users sign up but are invisible to our growth/lifecycle tooling — we can't email them even though they consented to tracking.
- Intercom identity verification infrastructure (`gateway/analytics/intercom.go:11`, `GenerateIntercomHmacDigest`) exists but has no email to bind to because the server-side `Identify()` sends only a hashed `user-id` and a handful of non-PII traits.
- The same `Identify` payload feeds Mixpanel, PostHog, **and** Intercom through Segment, so the fix has to distinguish *who is allowed to receive PII* rather than stripping it globally.

## Goals

1. OSS users with analytics enabled (the default) are identifiable in Intercom by email and name within 1 minute of signup.
2. Users who explicitly choose anonymous tracking continue to send only hashed identifiers — no PII leaves the gateway.
3. Org-level admin control: org admins can switch their org between "identified" and "anonymous" analytics modes.
4. Existing Mixpanel/PostHog dashboards keep working — the hashed `user-id` property remains on all events so historical data joins cleanly.

## Non-goals

- Changing event taxonomy, property names, or dashboard definitions.
- Replacing Segment as the CDP.
- Sending PII for Enterprise/self-hosted customers that have `ProductAnalytics` disabled entirely (`analytics/runtime.go:14`) — those remain off.
- Backfilling Intercom with historical hashed users (separate one-time migration if desired).

## User Stories

- **As an OSS user**, when I sign up I receive a welcome email and can chat with the team in Intercom, because my email was captured on first identify.
- **As an OSS user who values privacy**, I can toggle my org to anonymous analytics and my email is no longer sent to any third-party destination.
- **As a growth/success engineer**, I can send Intercom campaigns (onboarding drip, churn-risk outreach) to users segmented by in-product behavior, because every identified user in Mixpanel/PostHog has a matching Intercom contact.

## Proposed Solution

### 1. Analytics Mode

Introduce a tri-state column on the org (or on `ServerMiscConfig` for self-hosted single-org installs):

```
analytics_mode: 'identified' | 'anonymous' | 'disabled'
```

- `identified` — **default for OSS cloud signups**. Server sends email + name to Intercom; Mixpanel/PostHog continue to get hashed `user-id` but also get `email` as an identified trait (they dedupe correctly on our hashed ID as the distinct_id).
- `anonymous` — current behavior. No PII leaves the gateway.
- `disabled` — no calls made at all (existing `IsAnalyticsEnabled()` path).

Default selection:
- Fresh OSS signup via `gateway/api/signup/signup.go` → `identified`.
- Self-hosted / Enterprise orgs → `anonymous` (preserves current behavior for existing deployments; admins can opt in).

Existing orgs on upgrade → `anonymous`. They opt in explicitly.

### 2. APIContext carries email

Thread `UserEmail` and `UserName` through `types.APIContext` (already populated at auth time via `models.User`, just not propagated to analytics). The Identify call in `gateway/analytics/segment.go:55-82` reads the mode from config and branches:

```go
func (s *Segment) Identify(ctx *types.APIContext) {
    if !shouldTrack(ctx) { return }

    traits := analytics.NewTraits().
        Set("org-id", ctx.OrgID).
        Set("user-id", getUserIDHash(ctx.UserID)).
        Set("is-admin", ctx.IsAdminUser()).
        Set("environment", s.environmentName).
        Set("status", ctx.UserStatus).
        Set("client-version", version.Get().Version)

    if ctx.AnalyticsMode == "identified" {
        traits = traits.
            SetEmail(ctx.UserEmail).
            SetName(ctx.UserName)
    }

    _ = s.Client.Enqueue(analytics.Identify{
        UserId:      getUserIDHash(ctx.UserID),
        AnonymousId: ctx.UserAnonSubject,
        Traits:      traits,
    })
    // Group call unchanged
}
```

The hashed `user-id` remains the canonical ID across all destinations. Email is a trait, not the primary key — so Mixpanel/PostHog user profiles stay stable across a mode flip.

### 3. Intercom wiring

Two complementary paths:

**a) Webapp client-side boot.** Browser boots Intercom with `{email, user_id: hashedID, user_hash: HMAC(email)}`. The gateway already exposes the HMAC endpoint; webapp needs to pass it into `intercomSettings` on load. This covers every user who opens the UI.

**b) Server-side on signup (covers CLI-only users).** Extend `identifySignup()` in `gateway/api/signup/signup.go:126` to also POST to Intercom's Contacts API when mode is `identified`, so a contact exists even if the user never opens the webapp. Use `HOOP_INTERCOM_API_KEY` env var loaded via `appconfig`.

### 4. Consent surface

- Signup flow: checkbox "Send me product updates and onboarding emails" (default checked). Unchecking sets the org to `anonymous`.
- Settings → Organization: radio toggle between Identified / Anonymous / Disabled with clear copy on what each sends.
- First-run banner on existing OSS orgs after upgrade: "Enable identified analytics to get support and onboarding via email."

### 5. Data retention / deletion

- When an org switches `identified → anonymous`, fire an Intercom `contact.archive` for every user in that org and a Segment `alias/delete` to scrub PII traits. User profile in Mixpanel/PostHog keeps its hashed ID.
- Honor `DELETE /v1/users/:id` by also deleting the Intercom contact.

## Out of scope / open questions

- **GDPR/DPA wording.** Legal needs to bless the default-on for OSS cloud. If we can't default-on, the "checkbox checked by default" in signup is the fallback — still better than today.
- **Self-hosted default.** Proposed `anonymous` to avoid surprising existing operators. Alternative: migration prompt on first admin login post-upgrade.
- **Email verification.** Do we require `user.verified = true` before sending to Intercom? Recommended yes — avoids polluting Intercom with unverified signups.
- **PostHog person profiles.** PostHog will start receiving `email` as a person property; decide whether to also send it as `$email` so their UI shows it, or keep person profiles keyed only by hashed ID.

## Implementation Checklist

1. Schema: add `analytics_mode` column (migration in `rootfs/app/migrations/`), default per environment.
2. `appconfig` / `ServerMiscConfig`: load analytics mode; expose on `APIContext`.
3. `gateway/analytics/segment.go`: branch Identify/Track on mode; always keep hashed `user-id`.
4. `gateway/api/signup/signup.go`: on `identified` signup, call Intercom Contacts API (server-side).
5. Webapp (`webapp/src/webapp/events/tracking.cljs`): boot Intercom with `{email, user_hash}` when mode is identified.
6. Settings UI: toggle + copy.
7. Deletion path: archive/delete Intercom contact on user delete and on mode downgrade.
8. Tests: unit coverage in `gateway/analytics/segment_test.go` for each mode; integration test of signup → Intercom contact creation (mocked).
9. Docs: update `.env.sample` (`HOOP_INTERCOM_API_KEY`), README analytics section.

## Success Metrics

- ≥ 80% of OSS signups in the first week have a matching Intercom contact within 60 seconds.
- Onboarding email open rate > 0 (currently literally 0 — we can't send).
- No increase in support tickets citing "unexpected email" or privacy concerns.
- Mixpanel/PostHog MAU counts stable ±2% through the rollout (proves the hashed ID still dedupes across the Identify mode change).

## Rollout

1. Ship behind feature flag `ANALYTICS_MODE_ENABLED`; default off.
2. Enable on staging; verify Intercom contact creation + Mixpanel dedupe.
3. Enable for new OSS cloud signups only (week 1).
4. Enable opt-in for existing OSS orgs via admin banner (week 2).
5. Document self-hosted opt-in path (week 3).
