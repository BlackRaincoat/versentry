package discord

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/notifier/format"
)

type webhookPayload struct {
	Content  string         `json:"content,omitempty"`
	Username string         `json:"username,omitempty"`
	Embeds   []webhookEmbed `json:"embeds,omitempty"`
}

type webhookEmbed struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Color       int    `json:"color,omitempty"`
}

func payloadFromEmbeds(embeds []Embed, username string) ([]byte, error) {
	apiEmbeds := make([]webhookEmbed, 0, len(embeds))
	for _, e := range embeds {
		apiEmbeds = append(apiEmbeds, webhookEmbed{
			Title:       e.Title,
			Description: e.Description,
			Color:       e.Color,
		})
	}
	p := webhookPayload{Embeds: apiEmbeds}
	if username != "" {
		p.Username = username
	}
	return json.Marshal(p)
}

func buildContentMessages(events []model.UpdateAvailable, instanceName, username string, simple bool) ([][]byte, error) {
	text, err := renderContentText(events, instanceName, simple)
	if err != nil {
		return nil, err
	}
	chunks := splitContent(text, maxContentLen)
	out := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		p := webhookPayload{Content: chunk}
		if username != "" {
			p.Username = username
		}
		body, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshal content payload: %w", err)
		}
		out = append(out, body)
	}
	return out, nil
}

func renderContentText(events []model.UpdateAvailable, instanceName string, simple bool) (string, error) {
	if simple {
		if len(events) != 1 {
			return "", fmt.Errorf("simple content expects one event, got %d", len(events))
		}
		data := format.ItemFromEvent(instanceName, events[0], false)
		return "📦 **" + EscapeMarkdown(data.Instance) + "**\n" + itemLine(data), nil
	}

	lines := make([]string, 0, len(events)+1)
	lines = append(lines, digestTitle(instanceName, len(events)))
	for _, event := range events {
		lines = append(lines, itemLine(format.ItemFromEvent(instanceName, event, false)))
	}
	return strings.Join(lines, "\n"), nil
}

func splitContent(text string, maxLen int) []string {
	if text == "" {
		return nil
	}
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		cut := maxLen
		if idx := strings.LastIndex(text[:cut], "\n"); idx > maxLen/2 {
			cut = idx + 1
		}
		chunks = append(chunks, strings.TrimRight(text[:cut], "\n"))
		text = strings.TrimLeft(text[cut:], "\n")
	}
	return chunks
}
