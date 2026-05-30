package ssr

import (
	"html/template"
	"strings"
)

// CDN host + pinned library versions for the lazy-loaded enrichment shims
// (ADR-0018). Pulling from jsdelivr keeps the binary small and reuses the
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
// CDN for the Mermaid/KaTeX shims (script + style + fonts) and the inline boot
// snippet, while keeping everything else same-origin.
const ContentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' https: data:; " +
	"style-src 'self' 'unsafe-inline' " + cdnBase + "; " +
	"font-src " + cdnBase + " data:; " +
	"script-src 'self' 'unsafe-inline' " + cdnBase

// bootScript builds the inline enrichment snippet, lazy-loading Mermaid and/or
// KaTeX from the CDN only for the placeholders the renderer reported. Returns
// empty when neither is present, so plain documents ship zero JS.
func bootScript(mermaid, math bool) template.HTML {
	if !mermaid && !math {
		return ""
	}

	var b strings.Builder
	b.WriteString("<script>(function(){")
	b.WriteString("function s(src,cb){var e=document.createElement('script');e.src=src;e.onload=cb;document.head.appendChild(e);}")
	if mermaid {
		b.WriteString("if(document.querySelector('.mermaid')){s('" + mermaidSrc + "',function(){mermaid.initialize({startOnLoad:true});});}")
	}
	if math {
		b.WriteString("if(document.querySelector('.math')){")
		b.WriteString("var l=document.createElement('link');l.rel='stylesheet';l.href='" + katexCSS + "';document.head.appendChild(l);")
		b.WriteString("s('" + katexJS + "',function(){document.querySelectorAll('.math').forEach(function(el){")
		b.WriteString("try{katex.render(el.textContent,el,{displayMode:el.classList.contains('math-display'),throwOnError:false});}catch(e){}")
		b.WriteString("});});}")
	}
	b.WriteString("})();</script>")
	return template.HTML(b.String())
}
