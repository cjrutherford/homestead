# How this guide is built

The guide is plain Markdown in the `docs/guide/` folder of the Homestead repository. It is for the owner of an appliance, not for developers.

## From Markdown to pages

`scripts/guide/build.py` turns the Markdown into static HTML. It uses only the Python standard library, so the build needs nothing installed. It supports headings, paragraphs, lists, code, links, tables and images. Raw HTML in the source is shown as text, never run. Links to other `.md` files become `.html` links, and the build fails if one points at a page that does not exist.

`docs/guide/_nav.txt` lists the pages in sidebar order.

Every page carries a strict Content-Security-Policy: no scripts, no fonts, no frames and no network requests. The pages work offline, even opened from a folder.

## How it reaches your appliance

The built pages are embedded in the `homestead-server` binary, next to the dashboard. The appliance serves them at `/guide/`, so the guide always matches the version you run. Nothing is published to an external site, and the guide makes no outside requests.

Open the guide from the **Guide** link in the dashboard.

## Who can read it

Anyone signed in to the dashboard: the owner and people who have been given a sign-in. It is not available to visitors who are not signed in, on your home network or on a public name.

## Building it yourself

```
python3 -I scripts/guide/build.py
```

This writes the site to `internal/guide/dist/`. `make guide-build` does the same and `make release` runs it for you.
