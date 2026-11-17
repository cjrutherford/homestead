# S494 - Owner guide site

## Goal and dependencies
Owner 2026-11-08: docs are an owner guide only, Markdown in the repo, built by a small stdlib-only Python script into static HTML that works offline, served by the appliance at `/guide/` so each owner has the guide for their version. Nothing is published externally. This slice is the builder, the serving and a skeleton; content is S495. Depends on S031 (embedded dashboard) and S338 (person sessions).

## Added
- `scripts/guide/build.py` (run `python3 -I scripts/guide/build.py`, or `make guide-build`): Markdown subset (headings, paragraphs, bullet/numbered lists with one nesting level, fenced code, blockquote, rule, tables, links, images, bold, italic, code spans). Raw HTML is escaped. Relative `.md` links become `.html` and the build fails on a missing target or a link leaving the guide; `javascript:`, `data:` and `//host` links are dropped; remote images are dropped. Navigation, previous/next and the footer version come from `docs/guide/_nav.txt` and `VERSION`. Each page has a strict CSP meta tag (`default-src 'none'`, local images, the one inline style by sha256, no script).
- `internal/guide`: `Handler` over any `fs.FS` (GET/HEAD only, no listing, no SPA fallback, CSP + nosniff + no-referrer headers), `embed.go` (`-tags embeddash`, `all:dist` so the committed `.gitkeep` keeps tagged builds compiling), `noembed.go`.
- Route `GET /guide/` (`Server.MountGuide`, `AppConfig.Guide`), classified session and added to the person allowlist.
- Guide link on the apps page and the administration sidebar foot.
- Build hooks: `make guide-build`, `make release`, CI embed step and `scripts/install-systemd.sh` (builds before `go build`, warns if python3 is missing).
- Skeleton `docs/guide/`: index with contents table, eight stub sections, "How this guide is built".

## Access decision
Same as the dashboard apps page: any signed-in owner or person, on the local name and the public name. The dashboard's static shell is public but carries no data; the guide is session class so it is not served to anonymous visitors and does not disclose the version to them. Anonymous requests get 401.

## Stub sections for S495
Getting started; Apps and the catalog; People and sign-in; Names, tunnel and public access; Storage and backups; Updates and releases; Troubleshooting; Command and page reference.

## Verification
`tests/test_s494_guide_builder.py`, `internal/guide/guide_test.go`, `internal/server/guide_mount_test.go`, dashboard specs (home, app), live on VM 2 (PLAT-DASH-3).
