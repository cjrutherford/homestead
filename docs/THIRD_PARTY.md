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
