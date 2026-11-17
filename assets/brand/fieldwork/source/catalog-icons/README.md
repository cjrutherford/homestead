# Fieldwork catalog illustrations

Provisional inventory from prior Homestead planning, not a verified checkout. Do not treat this list or IDs as authoritative. Home Assistant was optional. No claim is made that all seven applications currently ship.

Each icon is a custom functional illustration, not the upstream logo. Preserve upstream names and attribution; no third-party logo assets or fonts are bundled. SVG sources use a 64-unit grid; dark and light tiles have explicit colors, monochrome inherits currentColor when used inline. PNG exports are 32, 64, 128 and 256 px. Use at 32 px minimum for tiles; retain visible labels.

Before integration, enumerate actual catalog manifests and compare package IDs and current icon keys with manifest.json, inspect dashboard/src/app/ui/app-icons.ts, distinguish visible applications from infrastructure dependencies and plugins, add missing icons and remove unused mappings. Keep existing fallback, do not change manifest IDs or lifecycle behavior. Test both Lamplight themes and high contrast; use monochrome with theme foreground for high contrast. No code changes are authorized by this handoff.
