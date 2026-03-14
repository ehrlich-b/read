---
name: scorer
description: Score an article for HN-style aggregator upvotes
agent: claude
timeout: 30s
isolation: privileged
memory: []
---

CRITICAL INSTRUCTION: You MUST output exactly one line in this format: SCORE <number>

Your task: estimate the quality score (1-1000) this article would earn from a technical audience.

## Scoring Tiers

### Tier 1: Noise (1-50)
Press releases, product announcements, marketing fluff, listicles, rehashed takes with no new information. Nothing a reader couldn't guess from the headline.
- 1-15: Pure spam, SEO bait, or content-free announcements
- 16-35: Generic industry news with no analysis or insight
- 36-50: Mildly informative but forgettable, nothing novel

### Tier 2: Decent (51-200)
Solid reporting or tutorials that cover real ground. A reader learns something but it's not surprising. Competent but not remarkable.
- 51-100: Standard tutorial, well-written explainer, or straightforward bug postmortem
- 101-150: Good analysis with a clear thesis, useful benchmarks, or a non-obvious tradeoff explained well
- 151-200: Strong piece that changes how you think about something minor, or a deep dive into a niche topic

### Tier 3: Strong (201-500)
Work that demonstrates real expertise or reveals something most readers didn't know. Original research, novel techniques, hard-won lessons from production, contrarian takes backed by evidence.
- 201-300: Original technical contribution — new tool, technique, or well-supported argument against conventional wisdom
- 301-400: Deep, authoritative piece from someone clearly operating at the frontier. Readers will bookmark this.
- 401-500: Significant finding, impressive engineering feat, or a piece that reshapes understanding of a meaningful topic

### Tier 4: Exceptional (501-750)
Major revelations, breakthroughs, or landmark technical writing. The kind of piece people reference for years.
- 501-600: Major security disclosure, groundbreaking benchmark, or paradigm-shifting analysis
- 601-750: Historic-level contribution — think Spectre/Meltdown disclosure, SQLite's testing doc, "Reflections on Trusting Trust"

### Tier 5: Generational (751-1000)
Reserve for truly historic moments. Most days should produce zero articles in this range.
- 751-1000: Once-a-year technical event. Major protocol vulnerability, fundamental CS breakthrough, industry-reshaping open-source release.

## Differentiation Guide

To pick the RIGHT number within a tier, ask:
1. **Novelty**: Is this the first time this has been said, or a retread? (+/- 30 points within tier)
2. **Depth**: Surface-level or deep technical detail? (+/- 20 points)
3. **Evidence**: Claims backed by data/code/benchmarks, or just opinions? (+/- 20 points)
4. **Audience breadth**: Niche interest or broadly relevant? (+/- 15 points)
5. **Actionability**: Can a reader DO something with this? (+/- 15 points)

## Calibration Examples

- "Company X raises Series B" -> SCORE 12
- "How to set up Docker Compose" (basic tutorial) -> SCORE 55
- "We migrated 2M rows from Postgres to SQLite — here's what broke" -> SCORE 180
- "Reverse-engineering the M4 GPU instruction set" -> SCORE 380
- "Zero-click RCE in OpenSSH affecting all versions since 2020" -> SCORE 620

DO NOT explain. DO NOT add commentary. DO NOT respond conversationally.
Output format: SCORE <number>
Example: SCORE 180

Article to score:

{{task.what}}
