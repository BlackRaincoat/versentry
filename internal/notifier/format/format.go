package format

import (
	"html"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/imageweb"
	"github.com/BlackRaincoat/versentry/internal/model"
)

// Default Telegram templates (HTML).
const (
	// DefaultItemTemplate is used for item lines; instance is in the digest header (simple and digest).
	DefaultItemTemplate = `<b>{{.Container}}</b>: {{.Change}}{{if .URL}}
{{.URL}}{{end}}
`

	DefaultDigestTemplate = `📦 {{.Instance}}{{if gt .Count 1}} — {{.Count}} updates{{end}}
{{.Items}}`
)

// ItemData is the template context for one update.
type ItemData struct {
	Instance   string
	Container  string
	Image      string
	Tag        string
	Change     string
	URL        string
	CurrentTag string
	LatestTag  string
	Host       string
}

// DigestData is the template context for a digest batch.
type DigestData struct {
	Instance string
	Count    int
	Items    string
}

// UpdateEntry is one element in the default webhook JSON payload.
type UpdateEntry struct {
	Container  string `json:"container"`
	Image      string `json:"image"`
	Host       string `json:"host"`
	CurrentTag string `json:"current_tag"`
	LatestTag  string `json:"latest_tag,omitempty"`
	Change     string `json:"change"`
	URL        string `json:"url,omitempty"`
	Mode       string `json:"mode"`
}

// Payload is the default webhook JSON envelope.
type Payload struct {
	Instance string        `json:"instance"`
	Count    int           `json:"count"`
	Updates  []UpdateEntry `json:"updates"`
}

// ItemFromEvent builds template/JSON fields for one update.
func ItemFromEvent(instanceName string, event model.UpdateAvailable, escapeHTML bool) ItemData {
	mode := TrackingMode(event)
	tagForURL := event.CurrentTag
	if event.LatestTag != "" {
		tagForURL = event.LatestTag
	}
	link := imageweb.URL(event.Host, event.Repo, tagForURL, event.Container.Labels, mode)

	return ItemData{
		Instance:   escape(instanceName, escapeHTML),
		Container:  escape(event.Container.Name, escapeHTML),
		Image:      escape(event.Repo, escapeHTML),
		Tag:        escape(event.CurrentTag, escapeHTML),
		Change:     ChangeText(event, escapeHTML),
		URL:        escape(link, escapeHTML),
		CurrentTag: escape(event.CurrentTag, escapeHTML),
		LatestTag:  escape(event.LatestTag, escapeHTML),
		Host:       escape(event.Host, escapeHTML),
	}
}

// UpdateEntryFromEvent builds a webhook JSON update entry (no HTML escaping).
func UpdateEntryFromEvent(event model.UpdateAvailable) UpdateEntry {
	item := ItemFromEvent("", event, false)
	return UpdateEntry{
		Container:  item.Container,
		Image:      item.Image,
		Host:       item.Host,
		CurrentTag: item.CurrentTag,
		LatestTag:  item.LatestTag,
		Change:     item.Change,
		URL:        item.URL,
		Mode:       TrackingMode(event),
	}
}

// PayloadFromEvents builds the default webhook JSON envelope.
func PayloadFromEvents(instanceName string, events []model.UpdateAvailable) Payload {
	updates := make([]UpdateEntry, 0, len(events))
	for _, event := range events {
		updates = append(updates, UpdateEntryFromEvent(event))
	}
	return Payload{
		Instance: instanceName,
		Count:    len(events),
		Updates:  updates,
	}
}

// TrackingMode returns semver or digest for an update event.
func TrackingMode(event model.UpdateAvailable) string {
	if event.LatestTag != "" {
		return "semver"
	}
	return "digest"
}

// ChangeText formats the human-readable change line.
func ChangeText(event model.UpdateAvailable, escapeHTML bool) string {
	if event.LatestTag != "" {
		left := escape(event.CurrentTag, escapeHTML)
		right := escape(event.LatestTag, escapeHTML)
		return left + " → " + right
	}
	return "digest changed: " +
		escape(ShortDigest(event.LocalDigest), escapeHTML) +
		" → " +
		escape(ShortDigest(event.RemoteDigest), escapeHTML)
}

// ShortDigest formats a digest for display.
func ShortDigest(d string) string {
	hex := strings.TrimPrefix(d, "sha256:")
	if len(hex) > 12 {
		hex = hex[:12]
	}
	return hex + "…"
}

func escape(s string, escapeHTML bool) string {
	if !escapeHTML {
		return s
	}
	return html.EscapeString(s)
}
