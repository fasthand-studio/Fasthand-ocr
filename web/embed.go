// Package web holds the API documentation page served at the site root.
//
// The HTML is the single copy shared by the Go service and the legacy Worker,
// so the two implementations cannot drift apart.
package web

import _ "embed"

// Docs is the served documentation page.
//
//go:embed docs.html
var Docs []byte
