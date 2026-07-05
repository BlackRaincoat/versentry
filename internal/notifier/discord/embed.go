package discord

import (
	"fmt"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/notifier/format"
)

const (
	defaultEmbedColor     = 3447003 // Discord blurple
	maxEmbedDescription   = 4096
	maxEmbedTitle         = 256
	maxEmbedsPerMessage   = 10
	maxEmbedTotalChars    = 6000
	maxContentLen         = 2000
	embedItemSeparator    = "\n\n"
)

// Embed is one Discord embed object.
type Embed struct {
	Title       string
	Description string
	Color       int
}

func embedCharCount(e Embed) int {
	return len(e.Title) + len(e.Description)
}

func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func itemLine(data format.ItemData) string {
	line := "**" + EscapeMarkdown(data.Container) + "**: " + EscapeMarkdown(data.Change)
	if data.URL != "" {
		line += "\n" + EscapeMarkdown(data.URL)
	}
	return line
}

func digestTitle(instance string, count int) string {
	inst := EscapeMarkdown(instance)
	if count <= 1 {
		return truncateRunes("📦 "+inst, maxEmbedTitle)
	}
	return truncateRunes(fmt.Sprintf("📦 %s — %d updates", inst, count), maxEmbedTitle)
}

func simpleEmbed(event model.UpdateAvailable, instanceName string, color int) Embed {
	data := format.ItemFromEvent(instanceName, event, false)
	return Embed{
		Title:       truncateRunes("📦 "+EscapeMarkdown(data.Instance), maxEmbedTitle),
		Description: truncateRunes(itemLine(data), maxEmbedDescription),
		Color:       color,
	}
}

func buildSimpleEmbeds(events []model.UpdateAvailable, instanceName string, color int) []Embed {
	out := make([]Embed, 0, len(events))
	for _, event := range events {
		out = append(out, simpleEmbed(event, instanceName, color))
	}
	return out
}

func buildDigestEmbeds(events []model.UpdateAvailable, instanceName string, color int) []Embed {
	if len(events) == 0 {
		return nil
	}

	lines := make([]string, 0, len(events))
	for _, event := range events {
		lines = append(lines, itemLine(format.ItemFromEvent(instanceName, event, false)))
	}

	title := digestTitle(instanceName, len(events))
	return splitDigestLinesIntoEmbeds(title, lines, color)
}

func splitDigestLinesIntoEmbeds(title string, lines []string, color int) []Embed {
	if len(lines) == 0 {
		return nil
	}

	var embeds []Embed
	first := true
	var chunk []string

	flush := func() {
		if len(chunk) == 0 {
			return
		}
		desc := truncateRunes(strings.Join(chunk, embedItemSeparator), maxEmbedDescription)
		e := Embed{Description: desc, Color: color}
		if first {
			e.Title = truncateRunes(title, maxEmbedTitle)
			first = false
		}
		embeds = append(embeds, e)
		chunk = nil
	}

	for _, line := range lines {
		candidate := line
		if len(chunk) > 0 {
			candidate = strings.Join(append(append([]string(nil), chunk...), line), embedItemSeparator)
		}
		titleLen := 0
		if first {
			titleLen = len(title)
		}
		if len(candidate)+titleLen > maxEmbedDescription && len(chunk) > 0 {
			flush()
		}
		chunk = append(chunk, line)
		if len(strings.Join(chunk, embedItemSeparator))+titleLen > maxEmbedDescription {
			flush()
		}
	}
	flush()
	return embeds
}

func packEmbedsIntoMessages(embeds []Embed) [][]Embed {
	if len(embeds) == 0 {
		return nil
	}

	var batches [][]Embed
	var current []Embed
	currentChars := 0

	for _, e := range embeds {
		eChars := embedCharCount(e)
		if len(current) > 0 && (len(current) >= maxEmbedsPerMessage || currentChars+eChars > maxEmbedTotalChars) {
			batches = append(batches, current)
			current = nil
			currentChars = 0
		}
		current = append(current, e)
		currentChars += eChars
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}
