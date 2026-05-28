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

// RenderPage executes the page template and returns the full HTML document.
func RenderPage(data PageData) (string, error) {
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
