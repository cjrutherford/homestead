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
v0.8.0, deckcraft v0.7.0, vectorcraft v0.11.0 (v0.12.0 by S251), photocraft v0.9.0, lightcraft
v0.8.0, figcraft v0.6.0, filmcraft v0.8.0, effectcraft v0.10.0, soundcraft
v0.7.0, designcraft v0.8.0, cadcraft v0.7.0 (craft-launcher v0.4.1 was vendored in S253 and then dropped
from the catalog on 2026-10-17). Per-app entries (licence text, version,
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

### storytold/vectorcraft v0.12.0 (S251)
- Release: `https://github.com/storytold/vectorcraft/releases/tag/v0.12.0`
  (tag commit `8bc65329286dc1b84aa392997bb205613c3e96cb`, published 2026-10-16; still the latest release
  when S251 was implemented on 2026-10-17). VectorCraft was v0.11.0 at planning (2026-10-15); v0.12.0 was published 2026-10-16 and is the version vendored here.
- Artifact: `vectorcraft-web-0.12.0.zip`, 22585168 bytes,
  `https://github.com/storytold/vectorcraft/releases/download/v0.12.0/vectorcraft-web-0.12.0.zip`
- SHA-256: `83ef4aaf0d8e535d8a79224095b5f34ad83c22a0e0bfc4fd19f22784c912529a`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `vectorcraft-web-0.12.0/`, stripped by the
  shared Dockerfile): `index.html`, `vectorcraft-web-<hash>.js`, `vectorcraft-web-<hash>_bg.wasm` (64.7 MB uncompressed), `ASSETS.md`, `NOTICE`, a `licenses/pdf-backend/` tree of third-party licence texts (hayro crates, Foxit and CGATS notices), `LICENSE-APACHE`, `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and `.htaccess` (unused). No favicon file, no `fonts/` folder (the optional Arabic fonts folder in `HOSTING.md` is not in this build), no `parts.txt`, no `.part-*` files, no workers, no precompressed files.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the VectorCraft contributors, SHA-256 `8b6025dc4b707e0fb08b9c2054096f1be3ae61529399aa2c348dec676562dc71`; `LICENSE-APACHE`, the Apache License
  2.0 text, SHA-256 `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`;
  both ship inside the artifact and so inside the built image, and both
  equal the files at the tag in the source repository). Both read in full
  for the v0.12.0 release. Judgement: compatible with redistribution inside a
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
- NOTICE (source repository file at tag v0.12.0; the NOTICE also ships in the web artifact, so it is in the built image, SHA-256
  `cda4d86b58c5200204e786092ac634e8b60f9beda54219f334726f49e2065e82`). It lists bundled third-party material (Lucide icons under ISC, Source Sans 3, Source Serif 4, Inter and JetBrains Mono under the SIL OFL 1.1, egui default fonts, Adobe CMap resources under BSD-3-Clause, the patched hayro PDF crates under MIT OR Apache-2.0, Foxit standard-14 substitutes under BSD-3-Clause, a CC0 colour profile, the jpeg-encoder crate and the wasmi interpreter). The patched Windows 7 `windows-link` copy it mentions is not part of the web build. Captured verbatim:

```text
VectorCraft
Copyright (c) 2026 ArtCraft Team and the VectorCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ASSETS.md.

- Lucide icons (assets/icons/*.svg except dc-*.svg) — ISC License; the icons Lucide derived from Feather are also under the MIT License (Cole Bemis). See assets/icons/LICENSE-lucide.txt.
- Source Sans 3 and Source Serif 4 (assets/fonts/Source*) — SIL Open Font License 1.1. See assets/fonts/OFL-SourceSans3.txt, OFL-SourceSerif4.txt.
- Inter (assets/fonts/Inter-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-Inter.txt.
- JetBrains Mono (assets/fonts/JetBrainsMono-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-JetBrainsMono.txt.
- egui's default fonts, compiled into the app through the epaint_default_fonts crate — Ubuntu Light
  (Ubuntu Font Licence 1.0), Hack (MIT), Noto Emoji (SIL Open Font License 1.1) and emoji-icon-font
  (MIT). Their license texts ship with that crate.

Rust dependencies (vello_cpu, kurbo, peniko, skrifa, harfrust, usvg, linesweeper, egui, eframe, …) are
fetched through crates.io under their own permissive licenses (mostly MIT and/or Apache-2.0; also
BSD, ISC, Zlib, BSL-1.0 and Unicode-3.0).

JPEG export uses the jpeg-encoder crate (MIT OR Apache-2.0, and the IJG License): this software is
based in part on the work of the Independent JPEG Group.

WebAssembly plug-ins run in the wasmi interpreter (MIT OR Apache-2.0); its wasmparser dependency is
under the Apache License 2.0 with the LLVM exception.

The experimental Windows 7 build (packaging/windows/windows7.ps1) compiles a modified copy of the
windows-link crate 0.2.1 (vendor/windows-link; Microsoft, MIT OR Apache-2.0) that binds CoTaskMemFree
through ole32.dll. Its license texts are in vendor/windows-link and ship in that build's ZIP; every
other build uses the unmodified crate from crates.io.

Builds made with craft-fonts (the CRAFT_FONTS_DIR build option; all official releases) embed the
fonts listed in https://github.com/storytold/craft-fonts/blob/main/ATTRIBUTION.md (BIZ UDPGothic,
Shippori Mincho, BIZ UDMincho, Noto Sans CJK SC and Noto Sans Arabic in the current manifest;
web builds BIZ UDPGothic Regular only) under the SIL
Open Font License 1.1. Their license texts ship with the release packages as OFL-<family>.txt.

PDF import decodes CJK text with Adobe's CMap resources (https://github.com/adobe-type-tools/cmap-resources),
compiled into the app through the hayro-cmap crate under the BSD 3-Clause License:

Copyright 1990-2023 Adobe. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

Redistributions of source code must retain the above copyright notice,
this list of conditions and the following disclaimer.

Redistributions in binary form must reproduce the above copyright
notice, this list of conditions and the following disclaimer in the
documentation and/or other materials provided with the distribution.

Neither the name of Adobe nor the names of its contributors may be
used to endorse or promote products derived from this software without
specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

Shared PDF backend

The patched hayro-interpret 0.7.0, hayro-syntax 0.7.2 and hayro-jbig2 0.3.0
are fetched from https://github.com/storytold/craft-libs at revision
ca50d63b773c99f96e75c77ce5a1d21e7c9cda0d. Their upstream project is
https://github.com/LaurenzV/hayro, licensed MIT OR Apache-2.0. The snapshot retains each
crate's upstream licence files. The patch ledger is at
https://github.com/storytold/craft-libs/blob/ca50d63b773c99f96e75c77ce5a1d21e7c9cda0d/vendor/README.craft-reader.md.

Assets already bundled through Hayro's default features retain their licences:
- Foxit standard-14 substitutes: Copyright 2014 PDFium Authors. All rights reserved.
  Original code copyright 2014 Foxit Software Inc. BSD-3-Clause; see
  licenses/pdf-backend/hayro-interpret/assets/LICENSE_FOXIT.
- CGATS001Compat-v2-micro.icc: CC0-1.0, Clinton Ingram; see
  licenses/pdf-backend/hayro-interpret/assets/CGATS_LICENSE.txt.
- Predefined CMaps: Copyright 1990-2023 Adobe. All rights reserved. BSD-3-Clause,
  non-visual character-mapping data; see licenses/pdf-backend/hayro-cmap/LICENSE.txt.
  Adobe and contributor names do not endorse or promote this product.
Per-file hashes and sources are recorded in ASSETS.md.
```

  The per-file `ASSETS.md` (SHA-256 `aff82a234214efafae8f7f0037c4901dd23a56535326c589cd390dd4eb62ca40`) ships in the artifact and lives in the same repository at the same tag.
- Craft image: `homestead/craft-vectorcraft:0.12.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/photocraft v0.9.0 (S251)
- Release: `https://github.com/storytold/photocraft/releases/tag/v0.9.0`
  (tag commit `c699e3cda69f4852b43c0b191ab0091aaeff9d6a`, published 2026-10-14; still the latest release
  when S251 was implemented on 2026-10-17).
- Artifact: `photocraft-web-0.9.0.zip`, 11467035 bytes,
  `https://github.com/storytold/photocraft/releases/download/v0.9.0/photocraft-web-0.9.0.zip`
- SHA-256: `b3fa5ab6a9d32bc5ff7dfdde31939a2337471a196a2e70f1ff961a61b1bb02e6`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `photocraft-web-0.9.0/`, stripped by the
  shared Dockerfile): `index.html`, `photocraft-web-<hash>.js`, `photocraft-web-<hash>_bg.wasm` (25.0 MB uncompressed), `LICENSE-APACHE`, `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and `.htaccess` (unused). No `NOTICE` in the artifact, no favicon file, no `fonts/` folder (optional per `HOSTING.md`, absent here), no `parts.txt`, no `.part-*` files, no workers, no precompressed files.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the PhotoCraft contributors, SHA-256 `1a8f66d8a86b146f5336fa0d4b1186576c8aa98e60823080c85c7811cfd810af`; `LICENSE-APACHE`, the Apache License
  2.0 text, SHA-256 `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`;
  both ship inside the artifact and so inside the built image, and both
  equal the files at the tag in the source repository). Both read in full
  for the v0.9.0 release. Judgement: compatible with redistribution inside a
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
- NOTICE (source repository file at tag v0.9.0; not in the web artifact, SHA-256
  `fb7d6f0e57c396b6c94f6315d3dba13048a296d81e7546d33e2b7c77c71ea147`). It lists bundled third-party material (Inter and JetBrains Mono under the SIL OFL 1.1, Lucide icons under ISC, the SCOWL English word list under the SCOWL licence). The web artifact itself carries no `NOTICE`, only the two licence files, so the notice is captured here. Captured verbatim:

```text
PhotoCraft
Copyright (c) 2026 ArtCraft Team and the PhotoCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ATTRIBUTION.md.

Inter font (assets/fonts/Inter-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-Inter.txt.
JetBrains Mono font (assets/fonts/JetBrainsMono-Regular.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-JetBrainsMono.txt.
Lucide icons (assets/icons/*.svg, except slice-knife.svg) — ISC; icons derived from Feather also MIT. See assets/icons/LICENSE-lucide.txt.
SCOWL English word list (assets/dict/en_US-scowl-50.txt.gz) — SCOWL license. See assets/dict/LICENSE-SCOWL.txt.

Builds made with craft-fonts (CRAFT_FONTS_DIR; all official releases) also embed the fonts listed in
https://github.com/storytold/craft-fonts/blob/main/ATTRIBUTION.md (today BIZ UDPGothic, Shippori Mincho and
BIZ UDMincho; not the web build), under the SIL Open Font License 1.1. Their licence texts
ship with the package as OFL-<family>.txt.
```

  The per-file `ATTRIBUTION.md` (SHA-256 `6f5464b818b1273eea0592989d9f980eb8b31d3ccf8dde09067018a7cd210ad3`) lives in the same repository at the same tag.
- Craft image: `homestead/craft-photocraft:0.9.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/lightcraft v0.8.0 (S251)
- Release: `https://github.com/storytold/lightcraft/releases/tag/v0.8.0`
  (tag commit `16dbb90499d3c63a6582fcbba1b2550a6e2da032`, published 2026-10-14; still the latest release
  when S251 was implemented on 2026-10-17). LightCraft is marked experimental upstream: its library lives in the browser's own site storage, which the browser may clear.
- Artifact: `lightcraft-web-0.8.0.zip`, 66660218 bytes,
  `https://github.com/storytold/lightcraft/releases/download/v0.8.0/lightcraft-web-0.8.0.zip`
- SHA-256: `bfabb7edecfc8723b9d698ad2b6fa32e99fcdf977628a68b2884c4b06c6f017a`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `lightcraft-web-0.8.0/`, stripped by the
  shared Dockerfile): `index.html`, `lightcraft_web.js` and `lightcraft_web_bg.wasm` (27.3 MB uncompressed) with **unhashed names**, `worker.js` (a module render worker that re-instantiates the same wasm), `lightcraft_zh_hans.otf` (16.4 MB Simplified Chinese UI font), five `OFL-*.txt` font licences, `NOTICE`, `LICENSE-APACHE`, `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and `.htaccess` (unused). The artifact ships its own `.gz` and `.br` copies of the html, js, wasm, worker and font (the build overwrites the `.gz` copies it makes itself and keeps the font's `.gz`; the `.br` copies are inert, served as plain files if asked for). No `parts.txt`, no `.part-*` files.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the LightCraft contributors, SHA-256 `1470a5d44a88a9d385d9530763ea411ca199670d07f561a61d54256323972b92`; `LICENSE-APACHE`, the Apache License
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
- NOTICE (source repository file at tag v0.8.0; the NOTICE also ships in the web artifact, so it is in the built image, SHA-256
  `00a7cfc350859e56cb36d28d6e91e997dc1f67137496a1a4a299dd014ff74a56`). It lists bundled third-party material (Inter under the SIL OFL 1.1, public-domain showcase photographs not in the web build, camera matrices derived from Academy Software Foundation rawtoaces-data under Apache-2.0 and the CIE 1931 functions under CC-BY-4.0, inpainting code ported from PhotoCraft under MIT OR Apache-2.0, SAM 3 model code ported from Hugging Face Transformers under Apache-2.0, which makes `crates/segment` Apache-2.0 only, and craft-fonts under the SIL OFL 1.1). It also states what is **not** distributed: the SAM 3 weights (Meta's separate SAM License, downloaded by the user on request), optional AI denoise model weights (a RawNIND checkpoint under GPL-3.0, an opt-in offer that is off in the default build and downloaded by the user after accepting its terms) and GeoNames place names (CC-BY 4.0, downloaded on request). None of those ships in the artifact or the image, and Homestead neither enables nor downloads them. Captured verbatim:

```text
LightCraft
Copyright (c) 2026 ArtCraft Team and the LightCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in assets/ATTRIBUTION.md.

Inter (assets/fonts/Inter-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-Inter.txt.
Showcase photographs by Ansel Adams, Dorothea Lange and NASA (docs/images/) — public domain. See assets/ATTRIBUTION.md.

crates/raw/src/spectral_table.rs (lightcraft-raw) holds camera colour matrices derived from the
camera spectral sensitivities, training reflectances and model alias list of rawtoaces-data
(https://github.com/AcademySoftwareFoundation/rawtoaces-data, v1.1.0), an Academy Software
Foundation project licensed under the Apache License 2.0, and from the CIE 1931 colour-matching
functions it ships (CIE, CC-BY-4.0). The matrices were fitted by the LightCraft contributors
(2026) with tools/spectral_camera_table.py. See assets/ATTRIBUTION.md.

crates/inpaint (lightcraft-inpaint) ports the inpainting (PatchMatch completion) and Poisson membrane
fill code of PhotoCraft (https://github.com/storytold/photocraft, crates/algo/src/inpaint.rs and
poisson.rs), Copyright the PhotoCraft contributors, licensed under the MIT License or the Apache
License 2.0, at your option. The port was modified by the LightCraft contributors (2026); each
ported file says so.

crates/segment (lightcraft-segment) ports the SAM 3 model code of Hugging Face Transformers
(https://github.com/huggingface/transformers, src/transformers/models/sam3 and sam3_tracker),
Copyright The HuggingFace Team and Meta Platforms, Inc., licensed under the Apache License 2.0.
The port was modified by the LightCraft contributors (2026): translated from Python/PyTorch to
Rust on candle and restructured; each ported file says so. Because of this, crates/segment is
licensed under the Apache License 2.0 only (LICENSE-APACHE), not under the MIT option.

The SAM 3 weights are not part of LightCraft and are never distributed with it. A user downloads
the facebook/sam3 checkpoint only when they choose to (in the app, or by hand), under Meta's SAM
License (https://github.com/facebookresearch/sam3/blob/main/LICENSE), a separate, non-OSI
licence. See docs/ai-masks.md.

AI denoise weights are not part of LightCraft and are never distributed with it.
The default build offers user-supplied models only. The optional rawnind-model
feature offers a separately downloaded RawNIND UtNet2 Bayer checkpoint under
GPL-3.0 (https://www.gnu.org/licenses/gpl-3.0.html), after the user accepts its terms.
Commercial use is subject to those terms, including applicable source, licence and
notice obligations on redistribution. Enabling that offer in releases remains a
maintainer decision. The ONNX reader and CPU/GPU executor are our Rust implementation;
no darktable, RawSpeed, LibRaw or other GPL decoder/runtime code is included.

Builds made with craft-fonts (the optional CRAFT_FONTS_DIR build input; all official releases)
also embed the fonts listed in https://github.com/storytold/craft-fonts/blob/main/ATTRIBUTION.md
(the Japanese fonts BIZ UDPGothic, BIZ UDMincho and Shippori Mincho; the web build embeds only BIZ
UDPGothic Regular), under the SIL Open Font License 1.1. Release packages include each font's
licence as OFL-<family>.txt.

Place names for reverse geocoding (Photo > Fill Location from GPS) are not part of LightCraft and
are never distributed with it. On the user's request (Photo > Download Place Names) the app
downloads cities15000.zip, admin1CodesASCII.txt and countryInfo.txt from GeoNames
(https://download.geonames.org/export/dump/) into the settings folder. GeoNames data is licensed
under Creative Commons Attribution 4.0 (https://creativecommons.org/licenses/by/4.0/);
attribution: "GeoNames, geonames.org". The lookup code (crates/geocode) is our own.
```

  The per-file `assets/ATTRIBUTION.md` (SHA-256 `4b1497c3ea1afbf162c360708f0377896227b44d7a4141105f8a51dd0b9d6a0d`) lives in the same repository at the same tag.
- Craft image: `homestead/craft-lightcraft:0.8.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/figcraft v0.6.0 (S251)
- Release: `https://github.com/storytold/figcraft/releases/tag/v0.6.0`
  (tag commit `833f0d2c27f0acbae5394848930ae531fc89c724`, published 2026-10-14; still the latest release
  when S251 was implemented on 2026-10-17).
- Artifact: `figcraft-web-0.6.0.zip`, 8892594 bytes,
  `https://github.com/storytold/figcraft/releases/download/v0.6.0/figcraft-web-0.6.0.zip`
- SHA-256: `a414970e3857f6613efd7f90bd4c8394e5d51b31c4cf1fa0352f87820695b9c4`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `figcraft-web-0.6.0/`, stripped by the
  shared Dockerfile): `index.html`, `figcraft-web-<hash>.js`, `figcraft-web-<hash>_bg.wasm` (22.5 MB uncompressed), twenty `OFL-*.txt` licence texts for the fonts embedded in the wasm (no font files), `NOTICE`, `LICENSE-APACHE`, `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and `.htaccess` (unused). No favicon file, no `parts.txt`, no `.part-*` files, no workers, no precompressed files. The release also has a separate `figcraft-server-<version>-linux-*.tar.gz` that is not part of this package.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the FigCraft contributors, SHA-256 `360c12b61568ef50808e731c98ae84110ea6f674dc34ac1ecb05d7abb9a6079a`; `LICENSE-APACHE`, the Apache License
  2.0 text, SHA-256 `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`;
  both ship inside the artifact and so inside the built image, and both
  equal the files at the tag in the source repository). Both read in full
  for the v0.6.0 release. Judgement: compatible with redistribution inside a
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
- NOTICE (source repository file at tag v0.6.0; the NOTICE also ships in the web artifact, so it is in the built image, SHA-256
  `3de704e57b81ffcb2a699f21fcf912559b28cb1d0f1584a7fbb3a080b4b695d6`). It lists no bundled third-party fonts, icons or images in the repository; release builds embed the craft-fonts set under the SIL OFL 1.1 (their licence texts ship as `OFL-*.txt`), and Rust dependencies keep their permissive licences, including CDLA-Permissive-2.0 for the webpki-roots certificate list. Captured verbatim:

```text
FigCraft
Copyright (c) 2026 ArtCraft Team and the FigCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles no third-party fonts, icons or images in the repository: every icon is
drawn in code and original to FigCraft. The per-file list (author, source, license) of any bundled
asset is in ATTRIBUTION.md.

Release builds made with the optional craft-fonts build input (CRAFT_FONTS_DIR) embed the fonts
listed in ATTRIBUTION.md (Inter and others, SIL Open Font License 1.1); each package carries their
licence texts.

Rust crate dependencies keep their own licenses (MIT, Apache-2.0, BSD, ISC, Zlib, OFL for egui's
default fonts, CDLA-Permissive-2.0 for the root certificate list webpki-roots compiles in, and
similar permissive licenses); see each crate's metadata.
```

  The per-file `ATTRIBUTION.md` (SHA-256 `9ec4448e1327edcd1bf1793f6bf04564913c7653cd9398468d8a8c8a3d3b476b`) lives in the same repository at the same tag.
- Craft image: `homestead/craft-figcraft:0.6.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/filmcraft v0.8.0 (S252)
- Release: `https://github.com/storytold/filmcraft/releases/tag/v0.8.0`
  (tag commit `f03e21556d8e3a7155b91892d958debd5255d7af`, published 2026-10-14; still the latest release
  when S252 was implemented on 2026-10-17).
- Artifact: `filmcraft-web-0.8.0.zip`, 13326588 bytes,
  `https://github.com/storytold/filmcraft/releases/download/v0.8.0/filmcraft-web-0.8.0.zip`
- SHA-256: `6b13956fc741522427663517b1e483d6e0777891063fed02b1bd94bfada8a6c7`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `filmcraft-web-0.8.0/`, stripped by the
  shared Dockerfile): `index.html`, `filmcraft_web.js` (unhashed), `filmcraft_web_bg.wasm` (41.2 MB uncompressed, unhashed name), `audio-worklet.js`, `favicon.png`, `LICENSE-APACHE`, `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and `.htaccess` (unused). No `NOTICE` and no font licence texts, no service worker, no precompressed files, no `parts.txt`.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the FilmCraft contributors, SHA-256 `d2d8416f0883694c23b473e26372506e9e11f5f41b55a92b7388dab921d9a207`; `LICENSE-APACHE`, the Apache License
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
  `5761787313b9d6c13dd5f03da2e04209ad7bd08eff8920edfd184141d791195c`). The NOTICE is in the source repository only (it does not ship in the web artifact, and neither do the OFL font texts it lists); the built image therefore carries the MIT and Apache texts but not this NOTICE. It lists Inter, Noto Serif and JetBrains Mono (SIL OFL 1.1) as bundled fonts, plus craft-fonts (BIZ UDPGothic, Shippori Mincho, BIZ UDMincho, OFL 1.1) in release builds, so the release wasm embeds OFL-licensed fonts without their licence text in the artifact. Recorded as deferred work in `docs/DEFERRED.md`; this entry keeps the full NOTICE text for the owner. Captured verbatim:

```text
FilmCraft
Copyright (c) 2026 ArtCraft Team and the FilmCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ATTRIBUTION.md.

Inter font (assets/fonts/Inter-*.ttf) — OFL-1.1. See assets/fonts/OFL-Inter.txt.
Noto Serif font (assets/fonts/NotoSerif-Regular.ttf) — OFL-1.1. See assets/fonts/OFL-NotoSerif.txt.
JetBrains Mono font (assets/fonts/JetBrainsMono-Regular.ttf) — OFL-1.1. See assets/fonts/OFL-JetBrainsMono.txt.
Builds made with craft-fonts (CRAFT_FONTS_DIR; all official releases) also embed the fonts listed in
https://github.com/storytold/craft-fonts/blob/main/ATTRIBUTION.md (BIZ UDPGothic, Shippori Mincho,
BIZ UDMincho) — OFL-1.1. Their licence texts ship with the packages as OFL-<family>.txt.
Public-domain film and NASA frames and CC0 music shown in README screenshots (docs/images/) — public domain / CC0-1.0. See ATTRIBUTION.md.
```

- Craft image: `homestead/craft-filmcraft:0.8.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/effectcraft v0.10.0 (S252)
- Release: `https://github.com/storytold/effectcraft/releases/tag/v0.10.0`
  (tag commit `05955c08b4fd025f1a09882df72e6dbff1a8148a`, published 2026-10-14; still the latest release
  when S252 was implemented on 2026-10-17).
- Artifact: `effectcraft-web-0.10.0.zip`, 19031497 bytes,
  `https://github.com/storytold/effectcraft/releases/download/v0.10.0/effectcraft-web-0.10.0.zip`
- SHA-256: `b922abeda2420391a7fd887f53ebae6948730e63720bad6a6eb3dc48dc1b46a8`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `effectcraft-web-0.10.0/`, stripped by the
  shared Dockerfile): `index.html`, `effectcraft_web.js` and `effectcraft_web_bg.wasm` (62.5 MB uncompressed; names are the same in every release), `snippets/effectcraft-web-<hash>/js/host.js` (browser glue), `worker.js`, `audio-worklet.js`, `sw.js` (service worker with a precache list), `manifest.webmanifest`, `favicon.svg`, `icon-256.png`, `icon-512.png`, `LICENSE-APACHE`, `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and `.htaccess` (unused). No `NOTICE` and no font licence texts, no precompressed files, no `parts.txt`.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the EffectCraft contributors, SHA-256 `7b6db9a363688683feacdf9b528f059637f2e5d2edbacd8720fc9b26781c9abe`; `LICENSE-APACHE`, the Apache License
  2.0 text, SHA-256 `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`;
  both ship inside the artifact and so inside the built image, and both
  equal the files at the tag in the source repository). Both read in full
  for the v0.10.0 release. Judgement: compatible with redistribution inside a
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
- NOTICE (source repository file at tag v0.10.0, SHA-256
  `d0bf841555c56b6136745a61ec529c9a558c9dd7404d377d82f86d10e0e9b530`). The NOTICE is in the source repository only (it does not ship in the web artifact, and neither do the OFL font texts it lists); the built image therefore carries the MIT and Apache texts but not this NOTICE. It lists Inter, Noto Serif and JetBrains Mono (SIL OFL 1.1) as bundled fonts, which the release wasm embeds without their licence text in the artifact. Recorded as deferred work in `docs/DEFERRED.md`. Captured verbatim:

```text
EffectCraft
Copyright (c) 2026 ArtCraft Team and the EffectCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ATTRIBUTION.md.

Inter (assets/fonts/Inter-*.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-Inter.txt.
Noto Serif (assets/fonts/NotoSerif-Regular.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-NotoSerif.txt.
JetBrains Mono (assets/fonts/JetBrainsMono-Regular.ttf) — SIL Open Font License 1.1. See assets/fonts/OFL-JetBrainsMono.txt.
```

- Craft image: `homestead/craft-effectcraft:0.10.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/soundcraft v0.7.0 (S252)
- Release: `https://github.com/storytold/soundcraft/releases/tag/v0.7.0`
  (tag commit `3a28a657692c1b98b28c276d8f61df9471074f0f`, published 2026-10-14; still the latest release
  when S252 was implemented on 2026-10-17).
- Artifact: `soundcraft-web-0.7.0.zip`, 5939077 bytes,
  `https://github.com/storytold/soundcraft/releases/download/v0.7.0/soundcraft-web-0.7.0.zip`
- SHA-256: `b735de61fbdc21716977ace2649709bd307de832eda2d848f94d1868acef7c45`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also
  hashed independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `soundcraft-web-0.7.0/`, stripped by the
  shared Dockerfile): `index.html`, `soundcraft-web-<hash>.js`, `soundcraft-web-<hash>_bg.wasm` (15.5 MB uncompressed), `ai.storyteller.soundcraft-<hash>.png`, `NOTICE`, `ATTRIBUTION.md`, `LICENSE-APACHE`, `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and `.htaccess` (unused). No workers, no service worker, no precompressed files, no `parts.txt`.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the SoundCraft contributors, SHA-256 `6aff3a3f0d8eb4c2d4fca151a00cdf26d8ff3020889402a453381a12dba3381e`; `LICENSE-APACHE`, the Apache License
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
  `977949446409bf907b265d765f024d3194c09251c2a65a4ca418cf5cb2dc84c0`). The NOTICE also ships in the web artifact, so it is in the built image. It lists no bundled third-party material beyond the egui default fonts that live inside the egui crate under their own open licences. Captured verbatim:

```text
SoundCraft
Copyright (c) 2026 ArtCraft Team and the SoundCraft contributors.
Licensed under the MIT License or the Apache License, Version 2.0, at your option
(LICENSE-MIT, LICENSE-APACHE).

The ArtCraft name, wordmark and logos in docs/brand/ are trademarks of the ArtCraft Team. They are
not open source and are not covered by the license above; their terms are in
docs/brand/LICENSE-brand.txt.

Third-party material

This product bundles third-party material under its own licenses, listed below. The per-file
list (author, source, license) is in ATTRIBUTION.md.

None: every bundled asset is original to SoundCraft. Icons are drawn in code; demo audio is
synthesised in code. The egui default fonts (Ubuntu-Light, Hack, Noto Emoji, Emoji Icon Font) ship
inside the egui crate under their own open licenses (see ATTRIBUTION.md).
```

- Craft image: `homestead/craft-soundcraft:0.7.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/designcraft v0.8.0 (S253)
- Release: `https://github.com/storytold/designcraft/releases/tag/v0.8.0`
  (tag commit `11a51d5b665aec8d6e675bd107a8a01514f10d00`, published
  2026-10-14; still the latest release when S253 was implemented on
  2026-10-17).
- Artifact: `designcraft-web-0.8.0.zip`, 15306498 bytes,
  `https://github.com/storytold/designcraft/releases/download/v0.8.0/designcraft-web-0.8.0.zip`
- SHA-256: `f1ebe741d89949ba1d5069a50401f14b297b9a66d9d6cd2d8a88cd83e4c77888`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also hashed
  independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `designcraft-web-0.8.0/`,
  stripped by the shared Dockerfile): `index.html`,
  `designcraft-web-<hash>.js`, `designcraft-web-<hash>_bg.wasm` (39.6 MB
  uncompressed), `LICENSE-APACHE`, `LICENSE-MIT`, `README.md`, `HOSTING.md`,
  and the sample `_headers` and `.htaccess` (unused). No `NOTICE`, no
  workers, no service worker, no precompressed files, no `parts.txt`.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the DesignCraft contributors, SHA-256
  `d543d31e3120c64b70abc77cfadcbdc2249c78de1c8efa2e3d62540bb79de816`;
  `LICENSE-APACHE`, the Apache License 2.0 text, SHA-256
  `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`; both
  ship inside the artifact and so inside the built image, and both equal the
  files at the tag in the source repository). Both read for the v0.8.0
  release. Judgement: compatible with redistribution inside a
  Homestead-built image, on the same reasoning as WordCraft: the appliance
  builds the image locally and serves the unmodified release files (adding
  only `.gz` copies of the same bytes), the MIT option needs only that its
  copyright and permission notice travel with the files (they do), and
  nothing in either licence restricts the owner from rebuilding, inspecting
  or running the image without Homestead.
- Trademarks: the ArtCraft name, wordmark and logos (`docs/brand/` in the
  source repository, not in the web artifact) are the ArtCraft Team's and are
  outside the licence. Homestead ships none of them; the package copy names
  the app by function and does not use the marks. Adobe and InDesign are not
  used in the package text.
- NOTICE (source repository file at tag v0.8.0, SHA-256
  `59da5d57095806a032148fb768b257b90f9f2de89171d7c3f6c48bf4fea7258e`). The
  web zip does not ship the NOTICE or the `OFL-*.txt` font licence texts it
  names, so the built image carries only the MIT and Apache texts (same gap
  as FilmCraft and EffectCraft, `docs/DEFERRED.md`). Captured in summary; the
  verbatim file is at the tag. Third-party material it lists: Source Sans 3,
  Source Serif 4, Inter and JetBrains Mono fonts (SIL OFL 1.1); an English
  hyphenation word list (derived from the public-domain Moby Hyphenator II)
  and trained patterns (CC0-1.0); Spanish hyphenation patterns (hyph-es,
  MIT) and Portuguese patterns (hyph-pt, BSD 3-clause) through the `hypher`
  crate; Japanese fonts BIZ UDPGothic, BIZ UDMincho and Shippori Mincho from
  craft-fonts (SIL OFL 1.1) in official releases; UI icons drawn in code;
  the `hayro` PDF crates (Apache-2.0 OR MIT, patched copies from storytold
  pdfcraft) with PDFium's Foxit standard-14 fonts (BSD-style) and a CMYK
  profile (CC0-1.0); Rust dependencies under MIT, Apache-2.0, BSD, ISC or
  Zlib. All permissive; no copyleft.
- Craft image: `homestead/craft-designcraft:0.8.0`, built locally by S279
  from this artifact and `images/craft/` (nginx base pinned above).

### storytold/cadcraft v0.7.0 (S253)
- Release: `https://github.com/storytold/cadcraft/releases/tag/v0.7.0`
  (tag commit `fc4e98315a8d771b5ab832f0ade0b5e7b106df7f`, published
  2026-10-14; still the latest release when S253 was implemented on
  2026-10-17).
- Artifact: `cadcraft-web-0.7.0.zip`, 8492921 bytes,
  `https://github.com/storytold/cadcraft/releases/download/v0.7.0/cadcraft-web-0.7.0.zip`
- SHA-256: `47df17081e31bc25ab586ba88156dc25e07962364c73513004b9c6113b575be7`,
  equal to the line for this file in the release's unsigned
  `SHA256SUMS.txt` (cross-checked 2026-10-17; the download was also hashed
  independently). Trust rests on the signed Homestead craft manifest
  (`catalog/craft/craft-manifest.yaml`), which records this digest.
- Archive contents (one top-level directory `cadcraft-web-0.7.0/`, stripped
  by the shared Dockerfile): `index.html`, `cadcraft-web-<hash>.js`,
  `cadcraft-web-<hash>_bg.wasm` (22.5 MB uncompressed), `LICENSE-APACHE`,
  `LICENSE-MIT`, `README.md`, `HOSTING.md`, and the sample `_headers` and
  `.htaccess` (unused). No `NOTICE`, no workers, no service worker, no
  precompressed files, no `parts.txt`.
- Licence: `MIT OR Apache-2.0`, at the recipient's option (`LICENSE-MIT`,
  Copyright (c) 2026 ArtCraft Team and the CADCraft contributors, SHA-256
  `54b0fc87ed7b61b59c20340070dd2b8c7f9d0b53795347819c3bd7c67ea2508a`;
  `LICENSE-APACHE`, SHA-256
  `0d542e0c8804e39aa7f37eb00da5a762149dc682d7829451287e11b938e94594`; both
  ship inside the artifact and equal the files at the tag). Both read for
  the v0.7.0 release. Judgement: compatible with redistribution inside a
  Homestead-built image, on the same reasoning as WordCraft.
- **MPL-2.0 component.** The NOTICE discloses one weak-copyleft dependency:
  the `acadrust` crate (DWG reading and writing,
  `https://github.com/hakanaktt/acadrust`, Mozilla Public License 2.0), used
  unmodified as a separate library (`crates/dwg`, a dependency of the `io` crate; treated as compiled into the wasm, which is stripped and has no readable crate names to confirm).
  MPL-2.0 is file-level: it requires the source of the covered files to stay
  available and the licence notice to be kept, and does not reach the rest
  of CADCraft or Homestead. Its source is available from crates.io and the
  repository above, at version 0.6.4 (upstream `Cargo.lock` at the v0.7.0
  tag). Homestead does not modify the wasm and adds nothing
  to it. Judgement: compatible; the owner who redistributes the built image
  should keep this notice with it. The built image does not itself contain
  the MPL text or the crate source (the web zip ships neither).
- Trademarks: the ArtCraft marks (`docs/brand/`) are outside the licence and
  are not shipped. CADCraft is independent of and not endorsed by Autodesk;
  AutoCAD and DWG are Autodesk trademarks, and the package text does not use
  them.
- NOTICE (source repository file at tag v0.7.0, SHA-256
  `cdfbfa6606bc6c389758cfdc5addeccb47a2a2cc7bfb7fcc4be9a2b783bd1561`). Not in
  the web zip (deferred, as for DesignCraft). Summary: dual MIT or Apache-2.0
  with the ArtCraft marks excluded; the MPL-2.0 `acadrust` disclosure above;
  every bundled asset (icons, drafting font, hatch patterns, linetypes) is
  original to CADCraft and drawn or defined in code, with the per-file list
  in `ATTRIBUTION.md`; builds with the optional craft-fonts input embed SIL
  OFL 1.1 fonts; the Autodesk non-affiliation statement.
- Craft image: `homestead/craft-cadcraft:0.7.0`, built locally by S279 from
  this artifact and `images/craft/` (nginx base pinned above).

### storytold/craft-launcher v0.4.1 (S253) - not shipped
Removed from the catalog and the craft manifest by owner decision on
2026-10-17 (see `docs/DEFERRED.md`, "Craft apps start page"). Homestead no
longer redistributes this release, so no licence, trademark or NOTICE
obligations arise from it. It was vendored only during S253 (artifact
`artcraft-launcher-web-0.4.1.zip`, SHA-256
`5768d88f6077375b926723cdf7ee56717997b57e99c2e419cbe89b527b497a27`) and the
KVM validation of that slice.

## Atkinson Hyperlegible Next and Mono (S282)
Bundled dashboard typefaces: `AtkinsonHyperlegibleNext-Regular.woff2`,
`AtkinsonHyperlegibleNext-Bold.woff2` and
`AtkinsonHyperlegibleMono-Regular.woff2` in `dashboard/public/fonts/`,
served by the daemon with the dashboard (no runtime fetch from any external
host). Source: the Braille Institute's Atkinson Hyperlegible Next, published
on GitHub by Google Fonts (`https://github.com/googlefonts/atkinson-hyperlegible-next`
and `https://github.com/googlefonts/atkinson-hyperlegible-next-mono`,
`fonts/webfonts/`, main branch, retrieved 2026-10-18). Licence: SIL Open
Font License 1.1 (Copyright 2020-2024 The Atkinson Hyperlegible Next Project
Authors and The Atkinson Hyperlegible Mono Project Authors); the OFL texts
ship beside the fonts as `OFL-Atkinson-Hyperlegible-Next.txt` and
`OFL-Atkinson-Hyperlegible-Mono.txt`. The files are unmodified.


## Barlow Semi Condensed SemiBold (S309)
Bundled dashboard display face: `BarlowSemiCondensed-SemiBold.woff2` in
`dashboard/public/fonts/` (weight 600, normal, Latin subset), used only for
headings, the wordmark, the permit title and the approval stamp. Served by
the daemon with the dashboard (no runtime fetch from any external host).
Source: the Barlow Project (`https://github.com/jpt/barlow`), taken from the
npm package `@fontsource/barlow-semi-condensed` 5.3.0
(`files/barlow-semi-condensed-latin-600-normal.woff2`, retrieved 2026-10-25,
SHA-256 `f158417e9207b5362f9b71a2fe779ce5bb836ad972f38445c3163af39d2c998d`);
no package scripts were run. Licence: SIL Open Font License 1.1 (Copyright
2017 The Barlow Project Authors); the OFL text ships beside the font as
`OFL-Barlow.txt`. The file is unmodified.

## Everything image base images (S367, ADR-053)
`images/everything/Dockerfile` (ADR-057) pins by index digest (recorded 2026-11-02):
`python:3.13-alpine@sha256:7984cf56ceaddebe4fefeb75d64000b9f45d8b77b11a09b24524da6cdc3af0c8` (PSF licence),
`node:24-alpine@sha256:8a32c8cf649a49915a66e81e7a609223c708d0adabf288ee3f4077d765542f85` (MIT),
`golang:1.26-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308` (BSD-3-Clause),
`alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f` (build-only), and the runtime
`gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab` (Apache-2.0 with Debian package licences). Updating a digest is a reviewed change.

## Projects source (S369, ADR-057)
`third_party/everything/projects-source.tar.gz` is the `src/projects` subtree plus
the root `LICENSE` and `dev/puter-localhost.mjs` of HeyPuter/everything at commit
`e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`, built as a deterministic archive (GNU
tar, sorted names, mode 0664, uid/gid 0, mtime 0, `gzip -n`; 137 entries). SHA-256
`368795cb9e68e8e8fb70095459b2586b409c3a816bf5fb6cba1031881748b981`, recorded in
`third_party/everything/pin.yaml`. Licence: MIT (see the `LICENSE` file inside the
archive). The source is unmodified; only `npm ci --ignore-scripts` and the build run.

## Records source (S370, ADR-057)
`third_party/everything/records-source.tar.gz` is the `src/records` subtree plus
the root `LICENSE` and `dev/puter-localhost.mjs` of HeyPuter/everything at commit
`e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`, built as a deterministic archive (GNU
tar, sorted names, mode 0664, uid/gid 0, mtime 0, `gzip -n`; 95 entries). SHA-256
`8ed5aa77f9264b341ec8bf0e07b41ca053f7eb3ba4edd224cc6f1472c3c26aa2`, recorded in
`third_party/everything/pin.yaml`. Licence: MIT (the root `LICENSE` inside the
archive; `src/records` has no licence file of its own and its `package.json`
declares MIT). It is not AGPL or copyleft. The only runtime dependency is
`lucide` 1.55.0 (ISC, bundled icons); the dev dependencies (vite, vitest, jsdom,
fake-indexeddb, axe-core) are build and test only. Upstream's own
`tools/check-licenses.mjs` runs as the `prebuild` step and fails the build on any
package outside its approved permissive list (73 packages passed). The source is
changed only by three user-facing wording patches listed in
`scripts/everything/apps/records.json`; only `npm ci --ignore-scripts` and the
build run.
