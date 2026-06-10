# ACP — Branding & Marketing Image Prompts

**Date:** 2026-06-10 · For: GitHub repo art + landing page + social. 15 prompts, each an
LLM image-generation brief written *to a human artist*, layer-by-layer, with the ideal
shape called out so the reshape-checker can crop/validate. Generate several options per
prompt; pick the on-brand winner.

---

## Brand bible (read first — every image must obey this)

**Product.** ACP — *Agent Coordination Protocol*. A shared filesystem + comms line that
lets multiple AI agents, on different machines, collaborate. Infra-grade: HA cluster,
mTLS, encryption, live co-editing. By **ab0t** (ab0t.com).

**Who it's for.** AI/infrastructure engineers building multi-agent systems; self-hosters;
people with taste for Tailscale / Linear / HashiCorp / Vercel branding. They trust *calm,
precise, confident* — not hype.

**Feeling (the north star).** Coordinated. Trustworthy. Quietly powerful. The relief of
two minds working in perfect sync over a shared surface. Premium, restrained, a little
futuristic — never loud.

**Core visual motif.** Two (or many) **nodes** = agents — softly glowing rounded squares
or orbs — joined by a **luminous conduit/link**. Beneath/between them a **shared surface**:
a subtle grid plane both nodes touch (the shared filesystem). Where the two links meet is
the **fusion point** — a small bright convergence (coordination/handshake). For HA, a clean
**mesh** of nodes. Think flat-with-depth or restrained isometric; soft volumetric glow;
generous negative space.

**Palette.**
- Base "Ink": `#0B0F14` → deep slate `#0E1420` (near-black, cool).
- Surface: `#11161F` / `#161D29`.
- Signature duo (the two agents): **Signal Teal** `#2DD4BF` and **Ember** `#FF8A5B`.
- Fusion (where they meet): **Violet** `#7C6CF0`.
- Text: off-white `#E6EDF3`; muted `#8B97A7`. Optional spark: cyan `#22D3EE`.

**Type (if any text is shown).** Geometric grotesk sans for words (Inter/Geist/Söhne
feel); monospace for labels/code (JetBrains Mono feel). Lowercase or sentence case,
confident, airy letter-spacing. Keep text minimal — usually leave clean negative space and
we'll set type ourselves.

**Always avoid.** Humanoid robots, glowing brains, cliché "AI faces," chrome blobs,
stock-photo people-at-laptops, neon cyberpunk overload, clutter, rainbow gradients, drop-
shadow kitsch. If it looks like a 2015 AI startup, it's wrong.

**Judging mindset.** Treat each like a photography/graphic-design competition entry:
one clear idea, strong composition, real emotion, instantly on-brand, value obvious at a
glance.

---

## 1. Primary logo mark — the symbol
**SHAPE:** 1:1 square, 1024×1024, centered, transparent or solid-ink background, generous
padding (mark fills ~62% of frame; safe margins all sides).

Design a single, memorable **symbol** that reads at 16px and at billboard size. I want two
rounded-square nodes — one Signal Teal, one Ember — connected by a short luminous conduit,
the two halves meeting at a bright Violet fusion point in the exact center, forming a
balanced, almost-monogram glyph that quietly suggests the letters "ACP" or an infinity/
handshake without spelling anything literally. **Layers:** (1) flat ink or transparent
base; (2) the two nodes as smooth matte shapes with a faint inner glow; (3) the conduit —
a clean tapered light-link; (4) the central fusion — a small radiant core with a soft
bloom; (5) a hairline geometric grid ghosted behind at 8% opacity hinting at the shared
surface. Flat vector feel with subtle depth, crisp edges, perfectly symmetrical, iconic.
It should feel **calm, exact, and trustworthy** — a maker's mark, not a sticker. No text.

## 2. Horizontal wordmark lockup — "acp"
**SHAPE:** 3:1 horizontal, 1500×500, mark left, wordmark right, strong negative space
right third for breathing room.

The mark from #1 at left, then the wordmark **"acp"** in a confident lowercase geometric
grotesk, off-white on ink, with airy tracking; a tiny monospace tagline beneath in muted
grey: *"agent coordination protocol."* **Layers:** (1) deep-ink background with a barely-
there vignette; (2) the symbol, its fusion point the only saturated color; (3) the
wordmark, optically aligned to the mark's height; (4) the tagline; (5) a faint horizontal
light-line linking mark to word, echoing the conduit. Premium, editorial, the restraint of
a HashiCorp/Linear lockup. Should feel **settled and certain.**

## 3. GitHub social preview / Open Graph card
**SHAPE:** exactly 2:1, **1280×640** (GitHub social preview spec), text-safe center.

The repo's first impression when shared. **Layers:** (1) ink gradient base, darkest at
edges; (2) a wide, shallow isometric shared-surface grid receding into soft fog; (3) two
agent nodes (teal + ember) on the grid, mid-distance apart, joined by a glowing conduit
that crosses dead-center with a Violet fusion flare; (4) a few faint data motes drifting
along the link to imply live traffic; (5) clean negative space across the top-center for an
overlaid headline (leave it empty — we set type). Bottom-left: room for a small "ab0t"
mark. Cinematic but minimal, shallow depth of field, volumetric glow. Feeling: **"two
machines, one shared mind — and it just works."**

## 4. Landing-page hero banner
**SHAPE:** 16:9, 1920×1080, focal subject right-of-center, large clean negative space on
the LEFT for headline + CTA.

The emotional centerpiece of the landing page. **Layers:** (1) deep-ink atmosphere with a
slow color temperature shift (cool left → faint violet right); (2) a sweeping, elegant
shared-surface plane in soft perspective, like a calm sea of faint grid light; (3) a
constellation of agent nodes — two prominent (teal, ember) in focus, a few smaller ones
softly out of focus — all tethered by luminous conduits converging toward a single radiant
Violet hub; (4) gentle particle flow along the links (the "comms line"); (5) atmospheric
god-rays and bloom; (6) deliberately empty left third for text. Awe with restraint — the
quiet sublime of infrastructure that holds. Feeling: **coordinated power, effortless
trust.** No text in the image.

## 5. README top banner
**SHAPE:** 4:1 ultra-wide, 1600×400, symmetrical, text-safe center band.

A slim hero for the top of README.md. **Layers:** (1) ink base; (2) a thin horizontal
shared-surface ribbon of grid light spanning the width; (3) evenly spaced agent nodes
along it, teal and ember alternating, linked node-to-node by conduits with small fusion
sparks between each pair (a "string of coordinated agents"); (4) faint flowing motes; (5)
soft top/bottom vignette; (6) clean center for the "acp" wordmark to be overlaid. Tidy,
rhythmic, calm. Should feel **dependable and in sync**, like a heartbeat line for a
healthy cluster.

## 6. Concept: "two agents, one shared canvas"
**SHAPE:** 4:3, 1600×1200, balanced two-subject composition.

Illustrate the core promise. **Layers:** (1) ink room with soft ambient light; (2) a single
luminous shared surface — a floating pane of faint grid, like a collaborative canvas seen
at a gentle angle; (3) two agent nodes, one teal (left) one ember (right), each extending a
clean light-tendril that *touches the same point* on the surface — both writing to one
shared file; (4) where their tendrils meet, a Violet ripple spreads across the grid
(convergence, merge); (5) a few soft floating tokens/glyphs implying files + messages; (6)
shallow depth, premium glow. The feeling I want: the **satisfying click of two
collaborators landing on the same answer.** No literal hands, no faces.

## 7. Concept: high-availability mesh (the cluster)
**SHAPE:** 16:9, 1920×1080, centered radial composition.

Sell resilience. **Layers:** (1) deep-ink space; (2) a balanced mesh of ~7 agent/server
nodes arranged in a calm constellation, interconnected by thin luminous links; (3) one
node subtly crowned as **leader** (brighter, a soft ring/halo), the others as followers;
(4) one node dimmed/"down" with its links instantly rerouting to a re-elected leader —
shown as a faint motion trail of light finding a new path (failover, no data lost); (5)
soft grid floor beneath grounding it; (6) gentle particle flow. Orderly, self-healing,
unbreakable-but-calm. Feeling: **"it stays up no matter what."** Geometry over chaos.

## 8. Concept: security — mTLS & encryption at rest
**SHAPE:** 1:1 square, 1200×1200, single strong focal object, centered.

Make "secure" feel *premium*, not padlock-cliché. **Layers:** (1) ink base with a tight
spotlight; (2) a single agent-node rendered as a faceted, vault-like form — smooth matte
shell with a faint engraved grid, suggesting an encrypted container; (3) between two such
forms, a luminous conduit sealed by a small geometric "key" glyph where they meet (mutual
TLS handshake) — only authenticated light passes; (4) inside the shell, a soft contained
glow (data encrypted, calm, locked); (5) fine particles repelled at the boundary (intrusion
denied). Restrained, architectural, expensive-looking. Feeling: **quiet, total
confidence — your data is held.** Avoid literal padlocks/shields.

## 9. Concept: real-time co-editing (CRDT convergence)
**SHAPE:** 16:9, 1920×1080, motion-from-edges-to-center composition.

Show live collaboration that never collides. **Layers:** (1) ink atmosphere; (2) a central
shared document-surface (faint grid + abstract lines of "text"); (3) two streams of light
entering from left (teal) and right (ember), each carrying small edit-tokens, flowing
toward the page simultaneously; (4) at the surface the two streams **interleave and merge
cleanly** into a single coherent Violet line — order from two sources, no conflict; (5)
soft trailing motion blur on the tokens to imply speed; (6) calm bloom. Feeling: the
**grace of two writers in perfect sync, zero friction.** Energetic yet harmonious.

## 10. Developer/CLI aesthetic hero
**SHAPE:** 16:9, 1920×1080, terminal slightly off-center, ambient space around.

For the "built for developers" section — make the terminal beautiful. **Layers:** (1) ink
desk-space with soft rim light; (2) a floating, glass-edged terminal panel (rounded, subtle
depth, faint inner glow) showing a few crisp monospace lines in brand colors — e.g. a teal
prompt, an ember response, a Violet "synced ✓" — abstracted so it reads as *vibe*, not real
copy (keep text sparse and legible-but-generic); (3) behind the panel, a ghosted shared-grid
surface and two distant nodes linked by a conduit, tying the CLI to the network; (4) a few
drifting motes; (5) gentle screen bloom + reflection. Clean, tactile, "tools I'd trust."
Feeling: **competent calm — this is serious, well-made infrastructure.**

## 11. Feature icon set (one sheet, consistent system)
**SHAPE:** 1:1 square, 1200×1200, tidy 3×3 grid of 9 icons, equal spacing.

A cohesive icon family for feature rows. **Layers:** (1) ink or transparent base; (2) a 3×3
grid of minimalist line+glow icons, identical stroke weight, rounded joints, each in
mono-line off-white with a single brand-color accent: shared-filesystem (stacked grid
plane), comms/mailbox (envelope-as-node), event log (pulse line), lease/lock (key ring),
CRDT merge (two arrows interleaving), spaces (nested frames), HA cluster (mesh), mTLS
(linked keys), MCP bridge (plug into node); (3) subtle, uniform inner glow on each; (4)
perfect alignment, equal optical size. Systematic, premium, Linear-grade icon discipline.
Feeling: **clarity and craft.**

## 12. App / favicon icon (simplified mark)
**SHAPE:** 1:1 square, 512×512, ultra-legible at 16–32px, bold safe margins.

The mark distilled for tiny sizes. **Layers:** (1) solid rounded-square ink tile (app-icon
silhouette) OR transparent; (2) a radically simplified version of the #1 symbol — essentially
the two node-dots + the central Violet fusion, fat strokes, no fine detail; (3) one soft
glow on the fusion point only. Must survive 16px: high contrast, chunky, instantly the same
brand as #1. Feeling: **a confident dot of coordination.** No text, no gradients-that-mud.

## 13. Abstract brand background / section texture
**SHAPE:** 16:9, 2560×1440 (also crops to wide strips), edge-safe (no focal subject — it's
a backdrop), tileable feel.

A reusable atmospheric background for landing sections and slide backdrops. **Layers:** (1)
deep-ink gradient field; (2) a vast, faint shared-surface grid in soft perspective fading to
black at the edges; (3) sparse, blurred bokeh nodes (teal/ember/violet) scattered with lots
of dark breathing room; (4) the faintest conduit lines threading between them; (5) gentle
film grain + vignette for depth. Must stay quiet enough that white headline text sits
cleanly on top anywhere. Feeling: **the calm hum of a system at rest, ready.** Mostly empty
on purpose.

## 14. Social announcement card (X/LinkedIn)
**SHAPE:** 1.91:1, 1200×630, focal right, headline negative space left.

For launch posts. **Layers:** (1) ink base, soft violet glow lower-right; (2) a confident
hero motif right-of-center — two prominent teal+ember nodes linked through a bright Violet
fusion over a small shared grid (the "money shot" of the brand in one glyph); (3) light
motes along the link; (4) bottom-right room for the small "acp / ab0t" lockup; (5) clean left
half for an overlaid one-line headline. Punchy, scroll-stopping, but unmistakably the same
restrained brand. Feeling: **"this is the thing your agents were missing."**

## 15. Vertical poster / mobile hero
**SHAPE:** 9:16 portrait, 1080×1920, vertical flow top→bottom, text-safe top + bottom bands.

A portrait key-visual for mobile landing + posters. **Layers:** (1) tall ink atmosphere,
cool top warming to faint violet at the base; (2) a vertical "spine" — a luminous conduit
rising up the center like a calm signal column; (3) agent nodes docking onto the spine at
intervals (teal/ember), each with a small fusion spark, a shared-grid ribbon threading
behind; (4) soft particle ascent implying flow and life; (5) deliberate empty bands top and
bottom for a stacked headline + CTA. Elegant, aspirational, gallery-poster quality.
Feeling: **upward momentum — coordinated agents, rising together.**

---

## Production notes (for the reshape-checker + pipeline)
- **Target shapes are explicit per prompt** (aspect + px). The checker should validate
  aspect first; if off, smart-crop toward the stated focal area (most have edge negative
  space for safe cropping), never stretch.
- **Consistency anchors across all 15:** ink base, the teal+ember→violet fusion motif, the
  shared-grid surface, soft volumetric glow, generous negative space, no text baked in
  unless noted (we typeset separately).
- **Deliver each in:** the native shape above + a 2:1 (1280×640) and 1:1 crop where the
  composition allows, so one render serves repo + social + icon needs.
- **File homes:** generated winners → `PUBLIC_REPO/assets/` (logo, og card, hero, banner,
  favicon, social) and the landing repo. Keep source prompts here (private).
