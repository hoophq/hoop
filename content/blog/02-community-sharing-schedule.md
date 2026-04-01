# Community Sharing & Distribution Schedule

## Overview

This schedule covers how to distribute the 6-month product update content across community channels. The goal is to maximize reach without spamming — each channel gets content tailored to its audience.

---

## Week 1: April 7-11 — Launch

### Day 1 (April 7): Newsletter + Blog + Twitter/X + LinkedIn
- [ ] Send newsletter email to customer list
- [ ] Publish blog post on hoop.dev/blog
- [ ] Post Twitter/X thread (overview)
- [ ] Post LinkedIn announcement
- [ ] Share in internal Slack/Discord

### Day 2 (April 8): Hacker News
- [ ] Submit blog post as "Show HN: 6 months of Hoop — RDP recording, AI session analysis, ABAC, and 70+ releases"
- [ ] Be available to answer comments for the first 2-3 hours
- [ ] **Angle:** Technical depth — mention wazero/Rust-in-Go, provider-agnostic AI client, ABAC junction tables

### Day 3 (April 9): Reddit
- [ ] r/devops: "We shipped RDP session recording, AI session analysis, and ABAC in the last 6 months — here's the full changelog"
- [ ] r/selfhosted: "Hoop v1.54.1 — 6 months of features for self-hosted users (RDP recording, audit logs, streaming sessions)"
- [ ] **Angle:** Self-hosted focus, upgrade path, migration notes

### Day 4 (April 10): Dev.to / Hashnode
- [ ] Cross-post blog to Dev.to (with canonical URL pointing to hoop.dev)
- [ ] Cross-post to Hashnode
- [ ] **Tags:** devops, security, infrastructure, golang, open-source

### Day 5 (April 11): Discord / Slack communities
- [ ] CNCF Slack #general or relevant channel
- [ ] DevOps Discord servers
- [ ] Infrastructure/Platform Engineering communities
- [ ] **Angle:** Brief message + link, not the full post

---

## Week 2: April 14-18 — Feature Spotlights

### Monday (April 14): AI Session Analyzer spotlight
- [ ] Twitter/X: AI session analyzer post
- [ ] LinkedIn: AI session analyzer post
- [ ] r/netsec: "We built an AI session analyzer that can block dangerous database commands before execution"

### Wednesday (April 16): ABAC spotlight
- [ ] Twitter/X: ABAC post
- [ ] LinkedIn: ABAC post

### Friday (April 18): Audit Logs spotlight
- [ ] Twitter/X: Audit logs post
- [ ] r/sysadmin: "Built audit logging with HTTP middleware that captures all admin write operations — here's how"

---

## Week 3: April 21-25 — Integration Stories

### Monday (April 21): Claude Code integration
- [ ] Twitter/X: Claude Code post
- [ ] LinkedIn: Claude Code post
- [ ] AI/ML communities: "Routing AI coding assistants through access control — Hoop now supports Claude Code"

### Wednesday (April 23): JIT Review Gates
- [ ] Twitter/X: JIT review gates post
- [ ] LinkedIn: Security-focused post about closing the native client credential gap

### Friday (April 25): SSH large file fix
- [ ] Twitter/X: SSH fix post (technical audience loves war stories)
- [ ] r/golang: "Debugging a goroutine leak in an SSH proxy — how unbounded processPacket() goroutines consumed hundreds of MBs" (if appropriate for the sub)

---

## Week 4: April 28 - May 1 — Wrap-up

### Monday (April 28): Infrastructure roundup
- [ ] Twitter/X: Infrastructure improvements post
- [ ] LinkedIn: Technical decision post (Go 1.24, wazero, Helm consolidation)

### Wednesday (April 30): Self-hosted CTA
- [ ] Twitter/X: Upgrade CTA
- [ ] LinkedIn: Upgrade CTA
- [ ] Re-share to r/selfhosted with upgrade focus

---

## Ongoing: May - June

### Weekly recycling (1x/week on Twitter/X)
Rotate top-performing posts with slight variations:
1. RDP recording
2. AI session analyzer
3. ABAC
4. Claude Code integration
5. SSH fix war story
6. Streaming sessions

### Bi-weekly LinkedIn (2x/month)
Feature spotlights tied to use cases:
- **Compliance:** Audit logs + RDP recording + mandatory metadata
- **Security:** ABAC + AI analyzer + JIT review gates
- **DevOps:** Runbooks v2 + streaming sessions + connection filters
- **AI governance:** Claude Code + AI analyzer + guardrails

### Monthly blog posts
Consider spinning off individual blog posts from the changelog for SEO:
1. "Building an RDP session recorder with Rust and Go" (technical deep dive)
2. "How we built an AI session analyzer with provider-agnostic LLM support" (architecture post)
3. "ABAC without the complexity: attribute-based policy grouping" (product post)
4. "Fixing a goroutine leak in our SSH proxy" (engineering war story)
5. "Securing AI coding assistants with infrastructure access controls" (thought leadership)

---

## Channel-Specific Guidelines

### Hacker News
- Lead with technical substance, not marketing
- Mention specific technical decisions (wazero, Go 1.24, GORM junction tables, gRPC flow control)
- Be honest about limitations ("Current implementation works but needs improvement on data storage" — direct from PR #1314)
- Respond to every comment for the first 3 hours

### Reddit
- Different subs want different angles — don't copy/paste the same post
- r/devops: workflow and operational benefits
- r/selfhosted: upgrade path and self-hosting specifics
- r/netsec: security features and threat model
- r/golang: technical implementation details
- Keep titles factual, avoid hype words

### Dev.to / Hashnode
- Use canonical URL to avoid SEO duplication
- Add a TL;DR at the top
- Include code snippets where possible (API examples, config examples)
- Tag appropriately

### Twitter/X
- Thread format for launch, single posts for spotlights
- Include specific numbers (lines of code, files changed, commit counts)
- Tag relevant accounts (@anthropaborgs for Claude Code, @golang for Go 1.24)

### LinkedIn
- Longer format is fine, people scroll
- Lead with the problem, then the solution
- Include "Why it matters" for each feature
- Tag team members who built the features

---

## Metrics to Track

- Newsletter open rate and click-through
- Blog post page views and time on page
- Social media impressions and engagement per post
- Hacker News upvotes and comment quality
- Reddit upvotes and saves
- Self-hosted upgrade rate after campaign (track in analytics)
- New GitHub stars during campaign period
