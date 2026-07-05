package discord

import "strings"

// discordMarkdownSpecial are characters that Discord treats as markdown formatting.
// Escape all of them so user-controlled text (container names, tags, URLs) renders literally.
var discordMarkdownSpecial = []string{"\\", "*", "_", "~", "|", ">", "`"}

// EscapeMarkdown prefixes Discord markdown metacharacters with a backslash.
func EscapeMarkdown(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		ch := string(r)
		for _, special := range discordMarkdownSpecial {
			if ch == special {
				b.WriteByte('\\')
				break
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}
