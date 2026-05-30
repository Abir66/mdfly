// Package ssr renders full HTML pages from pre-computed metadata and a rendered body.
package ssr

import (
	"bytes"
	_ "embed"
	"html/template"
)

//go:embed page.html.tmpl
var pageTemplateText string

var pageTmpl = template.Must(template.New("page").Parse(pageTemplateText))

// PageData holds the data needed to render a full HTML page.
type PageData struct {
	Title      string
	Excerpt    string
	OGImageURL string
	Body       template.HTML
}

// pageView is PageData plus the computed enrichment boot snippet passed to the
// template. BootScript is empty for documents with no Mermaid/KaTeX placeholders.
type pageView struct {
	PageData
	BootScript template.HTML
}

// RenderPage executes the page template and returns the full HTML document.
func RenderPage(data PageData) (string, error) {
	var buf bytes.Buffer
	view := pageView{PageData: data, BootScript: bootScript(string(data.Body))}
	if err := pageTmpl.Execute(&buf, view); err != nil {
		return "", err
	}
	return buf.String(), nil
}
