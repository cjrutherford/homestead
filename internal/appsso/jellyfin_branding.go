package appsso

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"strings"
)

// Jellyfin keeps its login-page branding in this file (BrandingOptions).
// The adapter edits it as a file, like the plugin's settings, because it has
// no Jellyfin admin session to use /System/Configuration/branding with.
const jellyfinBranding = "/config/config/branding.xml"

// Homestead's additions to the owner's disclaimer and CSS sit between these
// markers; nothing outside them is ever changed.
const (
	ssoHTMLOpen  = "<!-- homestead:sso -->"
	ssoHTMLClose = "<!-- /homestead:sso -->"
	ssoCSSOpen   = "/* homestead:sso */"
	ssoCSSClose  = "/* /homestead:sso */"
)

const ssoCSS = ".loginDisclaimer a.homestead-sso{display:block;margin:1em 0 0;padding:.9em 1em;text-align:center;" +
	"font-weight:500;text-decoration:none;border-radius:.2em;background:#00a4dc;color:#fff}" +
	".loginDisclaimer a.homestead-sso:hover{filter:brightness(1.1)}"

// jellyfinStartPath is where the plugin starts a sign-in, under the path
// Jellyfin serves on (its BaseUrl, the package's /apps/<name>/ path). The
// target's address is only an origin, so the path comes from the manifest
// (S331); an address with a path is used when the package has none.
func jellyfinStartPath(t Target) string {
	base := strings.TrimRight(t.Manifest.AppPath(), "/")
	if base == "" {
		if u, err := url.Parse(t.Address); err == nil {
			base = strings.TrimRight(u.Path, "/")
		}
	}
	return base + "/sso/OID/start/" + ProviderKey
}

func ssoHTMLBlock(startPath string) string {
	return ssoHTMLOpen + `<a class="homestead-sso" href="` + html.EscapeString(startPath) + `">` +
		html.EscapeString(ButtonText) + `</a>` + ssoHTMLClose
}

// withBlock replaces the marked block in text with block, or appends it;
// an empty block removes the marked block. Text outside it is untouched.
func withBlock(text, open, close, block string) string {
	if i := strings.Index(text, open); i >= 0 {
		if j := strings.Index(text[i:], close); j >= 0 {
			end := i + j + len(close)
			if block == "" {
				// Take the separating line break with the block.
				before, after := text[:i], text[end:]
				if strings.HasSuffix(before, "\n") && strings.HasPrefix(after, "\n") {
					after = after[1:]
				}
				return strings.TrimSuffix(before+after, "\n")
			}
			return text[:i] + block + text[end:]
		}
	}
	if block == "" {
		return text
	}
	if strings.TrimSpace(text) == "" {
		return block
	}
	return strings.TrimRight(text, "\n") + "\n" + block
}

type brandingExtra struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Inner   string     `xml:",innerxml"`
}

// brandingOptions mirrors Jellyfin's BrandingOptions; other elements are kept.
type brandingOptions struct {
	XMLName         xml.Name        `xml:"BrandingOptions"`
	LoginDisclaimer string          `xml:"LoginDisclaimer"`
	CustomCSS       string          `xml:"CustomCss"`
	Other           []brandingExtra `xml:",any"`
}

// brandingWith applies Homestead's block to a branding.xml document ("" when
// the file does not exist). startPath "" removes it. changed is false when
// nothing needs writing, so removal never creates the file.
func brandingWith(doc, startPath string) (out string, changed bool, err error) {
	var b brandingOptions
	exists := strings.TrimSpace(doc) != ""
	if exists {
		if err := xml.Unmarshal([]byte(doc), &b); err != nil {
			return "", false, fmt.Errorf("appsso: read Jellyfin's branding settings: %w", err)
		}
	}
	htmlBlock, cssBlock := "", ""
	if startPath != "" {
		htmlBlock, cssBlock = ssoHTMLBlock(startPath), ssoCSSOpen+ssoCSS+ssoCSSClose
	}
	newHTML := withBlock(b.LoginDisclaimer, ssoHTMLOpen, ssoHTMLClose, htmlBlock)
	newCSS := withBlock(b.CustomCSS, ssoCSSOpen, ssoCSSClose, cssBlock)
	if newHTML == b.LoginDisclaimer && newCSS == b.CustomCSS && (exists || startPath == "") {
		return doc, false, nil
	}
	b.LoginDisclaimer, b.CustomCSS = newHTML, newCSS
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(b); err != nil {
		return "", false, fmt.Errorf("appsso: render Jellyfin's branding settings: %w", err)
	}
	return buf.String() + "\n", true, nil
}

// writeBranding adds (startPath set) or removes Homestead's sign-in button
// in Jellyfin's login-page branding, keeping the owner's own text and CSS.
func (a jellyfin) writeBranding(ctx context.Context, t Target, startPath string) error {
	cur, err := execIn(ctx, t.Container, "", "", "sh", "-c", `cat "$1" 2>/dev/null || true`, "sh", jellyfinBranding)
	if err != nil {
		return fmt.Errorf("appsso: read Jellyfin's branding settings: %w", err)
	}
	doc, changed, err := brandingWith(cur, startPath)
	if err != nil || !changed {
		return err
	}
	write := `mkdir -p "$(dirname "$1")" && cat > "$1.tmp" && mv "$1.tmp" "$1"`
	if out, err := execIn(ctx, t.Container, "", doc, "sh", "-c", write, "sh", jellyfinBranding); err != nil {
		return fmt.Errorf("appsso: write Jellyfin's branding settings: %w: %s", err, out)
	}
	return nil
}
