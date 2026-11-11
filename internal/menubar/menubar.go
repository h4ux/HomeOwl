// Package menubar shows HomeOwl in the macOS menu bar: an owl icon (a few styles, optionally with a status dot
// or a problem count) whose popover is HomeOwl's own /menubar.html page. Elsewhere it does nothing.
package menubar

import _ "embed"

//go:embed icons/owl-outline.png
var owlOutline []byte

//go:embed icons/owl-filled.png
var owlFilled []byte

//go:embed icons/logo.png
var logo []byte

// Styles are the icon choices, in the order Settings lists them.
var Styles = []string{"outline", "dot", "filled", "logo", "count"}

// icon returns the picture for a style, whether macOS should tint it like its own icons, and whether a status dot goes on it.
func icon(style string) (png []byte, template, dot bool) {
	switch style {
	case "filled":
		return owlFilled, true, false
	case "logo":
		return logo, false, false
	case "dot":
		return owlOutline, true, true
	default: // outline, count
		return owlOutline, true, false
	}
}
