# Third-party dependencies

Dependencies added under an ADR carry their license here.

## filippo.io/age v1.3.2 (ADR-022)
Backup encryption, X25519 recipients. License: BSD-3-Clause
(Copyright The age Authors, Google LLC, Filippo Valsorda). Pulls in
`filippo.io/hpke` v0.4.0 (BSD-3-Clause, The Go Authors). The `age` CLI is a test and runbook prerequisite, not a runtime one.

## nginx 1.27-alpine base image (S279, ADR-028)
Base of the shared craft image (`images/craft/Dockerfile`), pulled once by
the local build and pinned by the multi-arch index digest
`nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10`
(recorded 2026-10-14). nginx is BSD-2-Clause (Copyright Igor Sysoev,
Nginx, Inc.); Alpine Linux components in the image carry their own
licences (MIT, GPL and others as packaged by Alpine). Homestead does not
redistribute the image: the appliance builds it locally. Changing the
digest changes the build-context digest and needs a re-signed craft
manifest.


## storytold craft suite (source recorded 2026-10-15, ADR-028)
Source: `https://github.com/storytold` (Apache-2.0 across the repositories,
per the owner's survey; each slice re-reads the licence and NOTICE of the
exact release it vendors). Each repository's GitHub release ships
`<app>-web-<version>.zip` and an unsigned `SHA256SUMS.txt`. Release URL
pattern: `https://github.com/storytold/<repo>/releases/download/<tag>/<asset>`.
Latest releases at planning: wordcraft v0.7.0, gridcraft v0.7.0, pdfcraft
v0.8.0, deckcraft v0.7.0, vectorcraft v0.11.0, photocraft v0.9.0, lightcraft
v0.8.0, figcraft v0.6.0, filmcraft v0.8.0, effectcraft v0.10.0, soundcraft
v0.7.0, designcraft v0.8.0, cadcraft v0.7.0, craft-launcher v0.4.1 (asset
`artcraft-launcher-web-0.4.1.zip`). Per-app entries (licence text, version,
artifact URL and SHA-256) are added by S249 to S253 as each app lands.

### storytold/wordcraft v0.7.0 (S249)
- Release: `https://github.com/storytold/wordcraft/releases/tag/v0.7.0`
  (tag commit `fc41918814f62645b6746ed68e71e8c2c2e85d6f`, published
  2026-10-14; still the latest release when S249 was implemented on
  2026-10-17).
- Artifact: `wordcraft-web-0.7.0.zip`, 15009187 bytes,
  `https://github.com/storytold/wordcraft/releases/download/v0.7.0/wordcraft-web-0.7.0.zip`
- SHA-256: `98df7c68a0ac788a9ac0f531fd34c31ab7d89965abb4935f57168d287dab3189`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17). Trust rests on the signed
  Homestead craft manifest (`catalog/craft/craft-manifest.yaml`), which
  records this digest.
- Archive contents (one top-level directory `wordcraft-web-0.7.0/`,
  stripped by the shared Dockerfile): `index.html`, the content-hashed
  `wordcraft-web-<hash>.js` and `wordcraft-web-<hash>_bg.wasm` (39.8 MB
  uncompressed), `wordcraft-<hash>.ico`, `LICENSE-APACHE`, `LICENSE-MIT`,
  `README.md`, `HOSTING.md`, and the sample `_headers` and `.htaccess`
  (unused). No `NOTICE`, `parts.txt` or precompressed files.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the WordCraft contributors, and
  `LICENSE-APACHE`, Apache License 2.0 text; both ship inside the
  artifact and so inside the built image). Both read in full for the
  v0.7.0 release. Judgement: compatible with redistribution inside a
  Homestead-built image. The appliance builds the image locally and serves
  the unmodified release files (adding only `.gz` copies of the same
  bytes), the MIT option needs only that its copyright and permission
  notice travel with the files (they do), and nothing in either licence
  restricts the owner from rebuilding, inspecting or running the image
  without Homestead. The planning note had expected Apache-2.0 alone; the
  release is dual-licensed, which is more permissive, not less.
- Trademarks: the ArtCraft name, wordmark and logos (`docs/brand/` in the
  source repository, not in the web artifact) are the ArtCraft Team's and
  are outside the licence (`docs/brand/LICENSE-brand.txt`, SHA-256
  `ed4475eacf972c9584aa335001c59dfa646eb222549b928ffd6aefd4c805b41a`). Homestead ships none of them in the
  image; the package copy names the app by function and does not use the
  marks.
- Upstream describes WordCraft as a clean-room reimplementation of a
  familiar word processor and carries a trademark disclaimer for the names
  it is compatible with. That is upstream's statement about its own
  provenance (ADR-028); Homestead's package text does not repeat it.
- NOTICE (source repository file at tag v0.7.0, SHA-256
  `8db865867d13975e1199a1ead02e61aeec45c7535cab0d6d48d039b8b6c72915`; not in the web
  artifact). It lists the bundled third-party material (fonts under the SIL
  OFL 1.1, hyphenation data under public domain, CC0, MIT and BSD-3-Clause)
  whose own notices are in the upstream repository. Captured verbatim:

```text
WordCraft
Copyright (c) 2026 ArtCraft Team and the WordCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ATTRIBUTION.md.

- Source Sans 3 and Source Serif 4 (assets/fonts/Source*) — SIL Open Font License 1.1. See assets/fonts/OFL-SourceSans3.txt, OFL-SourceSerif4.txt.
- Inter (assets/fonts/Inter-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-Inter.txt.
- JetBrains Mono (assets/fonts/JetBrainsMono-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-JetBrainsMono.txt.
- Japanese fonts (craft-fonts): builds made with craft-fonts (CRAFT_FONTS_DIR; all official
  releases) embed the fonts listed in
  https://github.com/storytold/craft-fonts/blob/main/ATTRIBUTION.md (BIZ UDPGothic, BIZ UDMincho,
  Shippori Mincho), under the SIL Open Font License 1.1. Their licence texts ship with the
  packages as OFL-<family>.txt. Builds without CRAFT_FONTS_DIR do not include them.
- English word list (assets/hyphenation/en-us.dic) — derived from the Moby Hyphenator II list by
  Grady Ward, public domain (Project Gutenberg eBook #3204). The hyphenation patterns
  (assets/hyphenation/en-us.pat) were trained from it and are released as CC0-1.0.
- Polish hyphenation patterns (assets/hyphenation/pl.pat) — hyph-pl.tex from the hyph-utf8
  package, Copyright (C) 1987-1995 Hanna Kołodziejska, Bogusław Jackowski, Marek Ryćko, bundled
  unmodified and used under the MIT License; the copyright and permission notice is in the file.
- Portuguese hyphenation patterns (assets/hyphenation/pt.pat) — hyph-pt.tex from the hyph-utf8
  package, bundled unmodified and used under the BSD 3-Clause License:

    Copyright (C) 1987, 1994, 1996, 2015 Pedro J. de Rezende, 1996, 2015 J. Joao Dias Almeida,
    2024 Leonardo Araujo and Aline Benevides

    Redistribution and use in source and binary forms, with or without modification, are
    permitted provided that the following conditions are met:
    * Redistributions of source code must retain the above copyright notice, this list of
      conditions and the following disclaimer.
    * Redistributions in binary form must reproduce the above copyright notice, this list of
      conditions and the following disclaimer in the documentation and/or other materials
      provided with the distribution.
    * Neither the name of the University of Campinas, of the University of Minho nor the names
      of its contributors may be used to endorse or promote products derived from this software
      without specific prior written permission.

    THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS
    OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
    MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL PEDRO
    J. DE REZENDE OR J.JOAO DIAS ALMEIDA BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
    EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE
    GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND
    ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
    NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED
    OF THE POSSIBILITY OF SUCH DAMAGE.
- Italian hyphenation patterns (assets/hyphenation/it.pat) — hyph-it.tex from the hyph-utf8
  package, Copyright (C) 2008-2011 Claudio Beccari, bundled unmodified and used under the MIT
  License (offered under LPPL 1.3+ or MIT); the copyright and permission notice is in the file:

    Copyright (C) 2008-2011 Claudio Beccari

    Permission is hereby granted, free of charge, to any person obtaining a copy of this
    software and associated documentation files (the "Software"), to deal in the Software
    without restriction, including without limitation the rights to use, copy, modify, merge,
    publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons
    to whom the Software is furnished to do so, subject to the following conditions:

    The above copyright notice and this permission notice shall be included in all copies or
    substantial portions of the Software.

    THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED,
    INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR
    PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE
    FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
    OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
    DEALINGS IN THE SOFTWARE.
- All UI icons are drawn in code (crates/ui-egui/src/icons.rs) and are original work, MIT OR Apache-2.0.

Rust dependencies (egui, eframe, vello_cpu, skrifa, harfrust, krilla, quick-xml, zip, image, …)
are permissively licensed (MIT/Apache-2.0/BSD/ISC/Zlib) and fetched through crates.io.

No Microsoft, Adobe, Avid or Autodesk icons, images, templates or artwork are included.
```

  The per-file `ATTRIBUTION.md` (SHA-256
  `2a28a5dae997462cce0b6bcb64d8f4de800f61ad3c6c09a33fdeec3f54ca13c2`) lives in the same repository at the same tag.
- Craft image: `homestead/craft-wordcraft:0.7.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/gridcraft v0.7.0 (S250)
- Release: `https://github.com/storytold/gridcraft/releases/tag/v0.7.0`
  (tag commit `d9d09f0baa912e88c5111107903fc8c640214b57`, published 2026-10-14; still the latest release
  when S250 was implemented on 2026-10-17).
- Artifact: `gridcraft-web-0.7.0.zip`, 7113559 bytes,
  `https://github.com/storytold/gridcraft/releases/download/v0.7.0/gridcraft-web-0.7.0.zip`
- SHA-256: `ad10af2baf9c7ed4ccbd81e32b37698f5075cdf4f26b726fee7fc7938a3a8d27`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `gridcraft-web-0.7.0/`, stripped by the
  shared Dockerfile): `index.html`, `gridcraft-web-<hash>.js`, `gridcraft-web-<hash>_bg.wasm` (18.9 MB uncompressed), `gridcraft-<hash>.ico`, `LICENSE-APACHE`,
  `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and
  `.htaccess` (unused). No `parts.txt`, no
  `.part-*` files, no workers, no precompressed files.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the GridCraft contributors, SHA-256 `a87109bbb1ad43ca3920a5d7ff3806863b7e8470a73f4ded97a1447ac93333f5`; `LICENSE-APACHE`, the Apache License
  2.0 text, SHA-256 `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`;
  both ship inside the artifact and so inside the built image, and both
  equal the files at the tag in the source repository). Both read in full
  for the v0.7.0 release. Judgement: compatible with redistribution inside a
  Homestead-built image, on the same reasoning as WordCraft: the appliance
  builds the image locally and serves the unmodified release files (adding
  only `.gz` copies of the same bytes), the MIT option needs only that its
  copyright and permission notice travel with the files (they do), and
  nothing in either licence restricts the owner from rebuilding,
  inspecting or running the image without Homestead.
- Trademarks: the ArtCraft name, wordmark and logos (`docs/brand/` in the
  source repository, not in the web artifact) are the ArtCraft Team's and
  are outside the licence. Homestead ships none of them in the image; the
  package copy names the app by function and does not use the marks.
- NOTICE (source repository file at tag v0.7.0, SHA-256
  `7c948d5b56639a6d53c362ce0b54a79badd6f272c542f0fc0f22e9ec31c61c2b`; not in the web artifact). It lists no third-party bundled assets in the web build (optional craft-fonts, SIL OFL 1.1, appear only in builds made with them). Captured verbatim:

```text
GridCraft
Copyright (c) 2026 ArtCraft Team and the GridCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ATTRIBUTION.md.

None: every bundled asset is original to GridCraft. Builds made with craft-fonts
(CRAFT_FONTS_DIR) embed the fonts listed in ATTRIBUTION.md under the SIL Open Font License 1.1.
```

  The per-file `ATTRIBUTION.md` (SHA-256
  `56b465eebb50e1b424c33291119529e45c8636b806d4432199ba7d57bc555c50`) lives in the same repository at the same tag.
- Craft image: `homestead/craft-gridcraft:0.7.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/pdfcraft v0.8.0 (S250)
- Release: `https://github.com/storytold/pdfcraft/releases/tag/v0.8.0`
  (tag commit `9931329b9fae0b05373b4dc3ac7c58541f0891a4`, published 2026-10-14; still the latest release
  when S250 was implemented on 2026-10-17).
- Artifact: `pdfcraft-web-0.8.0.zip`, 14113243 bytes,
  `https://github.com/storytold/pdfcraft/releases/download/v0.8.0/pdfcraft-web-0.8.0.zip`
- SHA-256: `1f9ea51f3c1f71cc719c12814e10f236912fa41ef54a7782c7dd4a619d9eee9a`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `pdfcraft-web-0.8.0/`, stripped by the
  shared Dockerfile): `index.html`, `pdfcraft-web-<hash>.js`, `pdfcraft-web-<hash>_bg.wasm` (38.1 MB uncompressed); no favicon file, `LICENSE-APACHE`,
  `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and
  `.htaccess` (unused), `NOTICE`. No `parts.txt`, no
  `.part-*` files, no workers, no precompressed files.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the PdfCraft contributors, SHA-256 `cd7032ea33b71427f188e34ffb4aee9d6868a57fd196037c5c8b2be03a2a0352`; `LICENSE-APACHE`, the Apache License
  2.0 text, SHA-256 `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`;
  both ship inside the artifact and so inside the built image, and both
  equal the files at the tag in the source repository). Both read in full
  for the v0.8.0 release. Judgement: compatible with redistribution inside a
  Homestead-built image, on the same reasoning as WordCraft: the appliance
  builds the image locally and serves the unmodified release files (adding
  only `.gz` copies of the same bytes), the MIT option needs only that its
  copyright and permission notice travel with the files (they do), and
  nothing in either licence restricts the owner from rebuilding,
  inspecting or running the image without Homestead.
- Trademarks: the ArtCraft name, wordmark and logos (`docs/brand/` in the
  source repository, not in the web artifact) are the ArtCraft Team's and
  are outside the licence. Homestead ships none of them in the image; the
  package copy names the app by function and does not use the marks.
- NOTICE (source repository file at tag v0.8.0, SHA-256
  `496946cff190a3fec32fe0241ea60d40539c73a9da1865073eb0c73ee822c8d5`; the NOTICE also ships in the web artifact). It lists bundled third-party material (Lucide icons under ISC, fonts under the SIL OFL 1.1, hayro and lopdf, Foxit standard-14 fonts and Adobe CMap resources under BSD-3-Clause, egui default fonts, a CC0 colour profile). The NOTICE also ships inside the artifact, so it is in the built image. Captured verbatim:

```text
PdfCraft
Copyright (c) 2026 ArtCraft Team and the PdfCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ATTRIBUTION.md.

- Lucide icons (assets/icons/*.svg) — ISC License. See assets/icons/LICENSE-lucide.txt.
  The Windows PDF file icon (assets/app-icon/pdfcraft-document.svg and .ico) adapts Lucide's
  file-text outline and combines it with contributor-original artwork. See
  assets/app-icon/LICENSE-document.txt (ISC AND (MIT OR Apache-2.0)), packaged as
  LICENSE-document.txt beside the Windows executables.
- Inter typeface (assets/fonts/Inter-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-Inter.txt.
- JetBrains Mono typeface (assets/fonts/JetBrainsMono-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-JetBrainsMono.txt.
- Dancing Script typeface (assets/fonts/DancingScript.ttf), Copyright 2016 The Dancing Script Project Authors — SIL Open Font License 1.1 (Reserved Font Name "Dancing Script"). See assets/fonts/OFL-DancingScript.txt.
- Fonts from craft-fonts (https://github.com/storytold/craft-fonts), an optional build input
  (CRAFT_FONTS_DIR) that is not part of this repository. Builds made with it, which includes every
  official release, embed the fonts listed in
  https://github.com/storytold/craft-fonts/blob/main/ATTRIBUTION.md (BIZ UDPGothic, Shippori Mincho,
  BIZ UDMincho; the web build only BIZ UDPGothic Regular) under the SIL Open Font License 1.1. Those
  packages carry each font's licence as OFL-<family>.txt. Builds made with the craft-fonts revision
  pinned in .github/workflows/ also include Noto Sans Thai (in the web build too), Copyright 2022
  The Noto Project Authors (https://github.com/notofonts/thai), under SIL OFL 1.1; its licence is
  fonts/noto-sans-thai/OFL.txt in the craft-fonts build input.
- hayro (rendering, via crates.io) — Apache-2.0 OR MIT. Its `embed-fonts` feature bundles the Foxit
  standard-14 Type 1 fonts under BSD-3-Clause ("Copyright 2014 PDFium Authors"), and hayro-cmap bundles
  Adobe CMap resources under BSD-3-Clause.
- lopdf (bootstrap document inspection, via crates.io) — MIT.
- egui default fonts (epaint_default_fonts, via eframe) — Ubuntu Light (Ubuntu Font Licence 1.0),
  Hack (MIT / Bitstream Vera licence), Noto Emoji (SIL OFL 1.1), emoji-icon-font (MIT).
- Colour profile in hayro-interpret — CGATS001Compat-v2-micro.icc (CC0-1.0, Clinton Ingram).

Required notices:

  Foxit standard-14 substitute fonts (vendor/hayro-interpret/assets/*.pfb):
    Copyright 2014 PDFium Authors. All rights reserved. Original code copyright 2014 Foxit Software Inc.
    Redistributed under the BSD-3-Clause licence in vendor/hayro-interpret/assets/LICENSE_FOXIT.

  CMap resources (bundled by hayro-cmap; character-mapping data, not visual assets):
    Copyright 1990-2023 Adobe. All rights reserved. Redistributed under the BSD-3-Clause licence
    (https://github.com/adobe-type-tools/cmap-resources/blob/master/LICENSE.txt). Neither the name of
    Adobe nor the names of its contributors are used to endorse or promote this product.

Showcase PDF (built by `cargo xtask demo-pdf`, not committed): fonts are downloaded at build time from
google/fonts at a pinned commit, verified by SHA-256, all under the SIL Open Font License 1.1 — see the
"Downloaded at build time" table in ATTRIBUTION.md.

Asset licences are not visible to cargo-deny; keep this file current whenever assets or asset-bundling
dependencies change (plan/adr/0002, 0006).

Vendored (vendor/, patched; see vendor/README.md):
- hayro 0.7.1 — Apache-2.0 OR MIT, Copyright the hayro authors (licence files kept in vendor/hayro).
- hayro-interpret 0.7.0 — Apache-2.0 OR MIT, Copyright the hayro authors (LICENSE-APACHE, LICENSE-MIT kept in vendor/hayro-interpret).
```

  The per-file `ATTRIBUTION.md` (SHA-256
  `1b324a5b8fabaccb9ee6bbb3cb889b6be82cacffe76de8e28b8f23d7d09c2f05`) lives in the same repository at the same tag.
- Craft image: `homestead/craft-pdfcraft:0.8.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/deckcraft v0.7.0 (S250)
- Release: `https://github.com/storytold/deckcraft/releases/tag/v0.7.0`
  (tag commit `d78e657e4cb1e490ceb5445785c052d9cf5ad963`, published 2026-10-14; still the latest release
  when S250 was implemented on 2026-10-17).
- Artifact: `deckcraft-web-0.7.0.zip`, 9385879 bytes,
  `https://github.com/storytold/deckcraft/releases/download/v0.7.0/deckcraft-web-0.7.0.zip`
- SHA-256: `4ab91994048c096fa356b7246c31098474d978ecd572e5b252dd825a287d1c40`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `deckcraft-web-0.7.0/`, stripped by the
  shared Dockerfile): `index.html`, `deckcraft-web-<hash>.js`, `deckcraft-web-<hash>_bg.wasm` (25.5 MB uncompressed), `ai.storyteller.deckcraft-<hash>.png`, `LICENSE-APACHE`,
  `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and
  `.htaccess` (unused). No `parts.txt`, no
  `.part-*` files, no workers, no precompressed files.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the DeckCraft contributors, SHA-256 `90e96fef569474cd526350e716e4b05dd32f179ca96d39d61e6b928485e35406`; `LICENSE-APACHE`, the Apache License
  2.0 text, SHA-256 `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`;
  both ship inside the artifact and so inside the built image, and both
  equal the files at the tag in the source repository). Both read in full
  for the v0.7.0 release. Judgement: compatible with redistribution inside a
  Homestead-built image, on the same reasoning as WordCraft: the appliance
  builds the image locally and serves the unmodified release files (adding
  only `.gz` copies of the same bytes), the MIT option needs only that its
  copyright and permission notice travel with the files (they do), and
  nothing in either licence restricts the owner from rebuilding,
  inspecting or running the image without Homestead.
- Trademarks: the ArtCraft name, wordmark and logos (`docs/brand/` in the
  source repository, not in the web artifact) are the ArtCraft Team's and
  are outside the licence. Homestead ships none of them in the image; the
  package copy names the app by function and does not use the marks.
- NOTICE (source repository file at tag v0.7.0, SHA-256
  `77b755b4c3c7c2076076547580001a2ecdefd270dcd6536ed8a7da1b64960a45`; not in the web artifact). It lists bundled third-party fonts (Ubuntu Light under the Ubuntu Font Licence 1.0; Hack, Noto Emoji and the egui emoji font under MIT, Bitstream Vera and OFL-1.1) and the code dependency symphonia (MPL-2.0, used unmodified, source at https://github.com/pdeljanov/Symphonia; the MPL file-level copyleft applies to symphonia's own files, which are unmodified, and imposes nothing on the rest). Captured verbatim:

```text
DeckCraft
Copyright (c) 2026 ArtCraft Team and the DeckCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ATTRIBUTION.md.

- Ubuntu Light (via the epaint_default_fonts crate) — Ubuntu Font Licence 1.0.
- Hack, Noto Emoji and the egui emoji icon font (via epaint_default_fonts) — MIT, Bitstream Vera,
  OFL-1.1.
- Builds made with craft-fonts (CRAFT_FONTS_DIR; all official releases) embed the fonts listed in
  ATTRIBUTION.md (Inter, Carlito, Caladea, Liberation, Montserrat, Lato, Open Sans, Roboto,
  Merriweather, Playfair Display, Poppins, Nunito Sans, BIZ UD, Shippori Mincho, Noto Sans
  Arabic) under the SIL Open Font License 1.1; their licence texts ship with release packages.

Code dependencies

- symphonia (audio decoding for MP3, AAC, ALAC, FLAC, Vorbis, WAV, AIFF, CAF) is used unmodified
  under the Mozilla Public License 2.0; its source is available at
  https://github.com/pdeljanov/Symphonia.
- The H.264, HEVC, VP9, AV1, Opus, MP4, Matroska and Ogg crates under crates/ are ported from
  FilmCraft (same owner, MIT OR Apache-2.0).
```

  The per-file `ATTRIBUTION.md` (SHA-256
  `e698cd8478b6b5d8b8dd78bc633948e50700435b57a6634a9f03bb1c6c6f285b`) lives in the same repository at the same tag.
- Craft image: `homestead/craft-deckcraft:0.7.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).
