# Brand assets — prompt → file map

Generated branding images live here. Source prompts: `../BRANDING_IMAGE_PROMPTS.md`
(v1, dark/premium) and `../BRANDING_IMAGE_PROMPTS_v2.md` (v2, cute cartoon — current
direction). Render v2 **#0 first** (mascot sheet), then keep every other image on-model.

A `reshape`/CI checker validates each file's aspect (smart-crop toward the focal area if
off; never stretch). Use the v2 cute set unless noted.

| Prompt # | File | Shape (px) | Use |
|----------|------|-----------|-----|
| v2 #0 | `mascot-sheet.png` | 1800×1200 (3:2) | character reference (source of truth) |
| v2 #1 | `logo-mark.png` | 1024×1024 (1:1) | primary logo mark |
| v2 #2 | `wordmark-lockup.png` | 1500×500 (3:1) | horizontal lockup |
| v2 #3 | `og-card.png` | 1280×640 (2:1) | GitHub social preview / OpenGraph |
| v2 #4 | `acp-hero.png` | **1215×472 banner (~2.57:1)** — current file | **README + landing hero** |
| v2 #5 | `readme-banner.png` | 1600×400 (4:1) | README top banner (alt) |
| v2 #6 | `concept-shared-canvas.png` | 1600×1200 (4:3) | "two buddies, one canvas" |
| v2 #7 | `concept-ha-mesh.png` | 1920×1080 (16:9) | HA cluster / failover |
| v2 #8 | `concept-security.png` | 1200×1200 (1:1) | mTLS + encryption |
| v2 #9 | `concept-crdt.png` | 1920×1080 (16:9) | live co-editing |
| v2 #10 | `concept-cli.png` | 1920×1080 (16:9) | dev/CLI hero |
| v2 #11 | `icons-feature-set.png` | 1200×1200 (1:1) | 9 feature icons |
| v2 #12 | `favicon.png` | 512×512 (1:1) | favicon / app icon |
| v2 #13 | `bg-texture.png` | 2560×1440 (16:9) | section background |
| v2 #14 | `social-card.png` | 1200×630 (1.91:1) | X/LinkedIn card |
| v2 #15 | `poster-vertical.png` | 1080×1920 (9:16) | mobile hero / poster |

The README hero references `assets/acp-hero.png` (prompt v2 #4). Drop the chosen render
there; provide a `<2x` retina version as `acp-hero@2x.png` if available.
