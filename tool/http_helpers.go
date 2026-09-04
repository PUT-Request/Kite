package tool

import "strings"

func previewBody(body []byte, max int) string {
	if len(body) <= max {
		return string(body)
	}
	return string(body[:max])
}

func trimPreview(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func escapeQuery(v string) string {
	v = strings.ReplaceAll(v, "%", "%25")
	v = strings.ReplaceAll(v, "&", "%26")
	v = strings.ReplaceAll(v, "=", "%3D")
	v = strings.ReplaceAll(v, "?", "%3F")
	v = strings.ReplaceAll(v, "#", "%23")
	v = strings.ReplaceAll(v, "+", "%2B")
	v = strings.ReplaceAll(v, " ", "%20")
	return v
}
