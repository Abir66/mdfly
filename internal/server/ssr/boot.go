package ssr

import (
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"strings"
)

// CDN host + pinned library versions for the lazy-loaded enrichment shims
// (ADR-0010). Pulling from jsdelivr keeps the binary small and reuses the
// libraries' own CDN caching.
const (
	cdnBase        = "https://cdn.jsdelivr.net"
	mermaidVersion = "11"
	katexVersion   = "0.16"

	mermaidSrc = cdnBase + "/npm/mermaid@" + mermaidVersion + "/dist/mermaid.min.js"
	katexJS    = cdnBase + "/npm/katex@" + katexVersion + "/dist/katex.min.js"
	katexCSS   = cdnBase + "/npm/katex@" + katexVersion + "/dist/katex.min.css"
)

// ContentSecurityPolicy is the CSP for SSR view pages. It allows the jsdelivr
// CDN for the Mermaid/KaTeX shims (script + style + fonts) and pins the inline
// boot snippet by SHA256 hash (one per deterministic variant) instead of
// 'unsafe-inline', while keeping everything else same-origin.
var ContentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' https: data:; " +
	"connect-src 'self'; " +
	"style-src 'self' 'unsafe-inline' " + cdnBase + "; " +
	"font-src " + cdnBase + " data:; " +
	"script-src 'self' " + scriptHash(railSnippet) + " " + bootScriptHashes() + " " + cdnBase

// scriptHash returns the CSP 'sha256-...' source for an inline script body.
func scriptHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// bootScriptHashes returns the space-separated CSP 'sha256-...' sources for
// every bootScript variant (mermaid-only, math-only, both), so the inline
// snippet is permitted without 'unsafe-inline'.
func bootScriptHashes() string {
	variants := [][2]bool{{true, false}, {false, true}, {true, true}}
	hashes := make([]string, 0, len(variants))
	for _, v := range variants {
		hashes = append(hashes, scriptHash(scriptBody(v[0], v[1])))
	}
	return strings.Join(hashes, " ")
}

// scriptBody builds the inline enrichment JS (no <script> wrapper), lazy-loading
// Mermaid and/or KaTeX from the CDN only for the placeholders the renderer
// reported. Returns empty when neither is present.
func scriptBody(mermaid, math bool) string {
	if !mermaid && !math {
		return ""
	}

	var b strings.Builder
	b.WriteString("(function(){")
	b.WriteString("function s(src,cb){var e=document.createElement('script');e.src=src;e.onload=cb;document.head.appendChild(e);}")
	if mermaid {
		b.WriteString("if(document.querySelector('.mermaid')){s('" + mermaidSrc + "',function(){mermaid.initialize({startOnLoad:false});mermaid.run();});}")
	}
	if math {
		b.WriteString("if(document.querySelector('.math')){")
		b.WriteString("var l=document.createElement('link');l.rel='stylesheet';l.href='" + katexCSS + "';document.head.appendChild(l);")
		b.WriteString("s('" + katexJS + "',function(){document.querySelectorAll('.math').forEach(function(el){")
		b.WriteString("try{katex.render(el.textContent,el,{displayMode:el.classList.contains('math-display'),throwOnError:false});}catch(e){}")
		b.WriteString("});});}")
	}
	b.WriteString("})();")
	return b.String()
}

// bootScript wraps scriptBody in a <script> tag. Returns empty when neither
// placeholder is present, so plain documents ship zero JS. The emitted snippet
// is pinned by ContentSecurityPolicy via bootScriptHashes.
func bootScript(mermaid, math bool) template.HTML {
	body := scriptBody(mermaid, math)
	if body == "" {
		return ""
	}
	return template.HTML("<script>" + body + "</script>")
}
