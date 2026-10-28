// SPDX-License-Identifier: Apache-2.0

package convert

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

type formField struct {
	name, value, contentType string
}

// multipartBody writes text fields as a multipart/form-data body, and
// returns it with its Content-Type. The boundary comes from the content,
// so the same fields always give the same body, and it never occurs in
// them.
func multipartBody(fields []formField) (string, string) {
	if len(fields) == 0 {
		return "", ""
	}
	var all strings.Builder
	for _, f := range fields {
		all.WriteString(f.name + "\x00" + f.value + "\x00")
	}
	sum := sha256.Sum256([]byte(all.String()))
	boundary := fmt.Sprintf("signalbox-%x", sum[:12])
	var b strings.Builder
	for _, f := range fields {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString(`Content-Disposition: form-data; name="` + escapeQuotes(f.name) + `"` + "\r\n")
		if f.contentType != "" {
			b.WriteString("Content-Type: " + f.contentType + "\r\n")
		}
		b.WriteString("\r\n" + f.value + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String(), "multipart/form-data; boundary=" + boundary
}

func escapeQuotes(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", " ", "\n", " ").Replace(s)
}
