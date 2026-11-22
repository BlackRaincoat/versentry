package format

import (
	"fmt"
	"html"
	"strings"
	"time"

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
	// Bump is major|minor|patch for tag updates and for digest updates with
	// semver OCI versions; empty otherwise. Optional in templates.
	Bump string
	// Reminder is true when this target was notified before (remind_after).
	Reminder bool
	// CurrentVersion / LatestVersion: OCI version labels of the running and the
	// new image (digest updates); empty when unknown.
	CurrentVersion string
	LatestVersion  string
	// Built is the new image's age ("3 days ago"); empty when unknown.
	Built string
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
	Bump       string `json:"bump,omitempty"`
	Reminder   bool   `json:"reminder,omitempty"`
	// CurrentVersion / LatestVersion: OCI version labels (digest updates).
	CurrentVersion string `json:"current_version,omitempty"`
	LatestVersion  string `json:"latest_version,omitempty"`
	// LatestCreated: build time of the new image, RFC 3339 UTC.
	LatestCreated string `json:"latest_created,omitempty"`
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
		Bump:       event.Bump,
		Reminder:   event.Reminder,

		CurrentVersion: escape(event.CurrentVersion, escapeHTML),
		LatestVersion:  escape(event.LatestVersion, escapeHTML),
		Built:          BuiltAgo(event),
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
		Bump:       event.Bump,
		Reminder:   event.Reminder,

		CurrentVersion: event.CurrentVersion,
		LatestVersion:  event.LatestVersion,
		LatestCreated:  createdRFC3339(event.LatestCreated),
	}
}

func createdRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
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
// Cross-major updates (tags, or OCI versions of a digest update) append
// " (major)"; minor/patch stay unmarked.
// Digest updates show OCI versions when known ("2.3.1 → 2.4.0", "2.3.1 rebuilt",
// "new image 2.4.0"), else the two short digests. A known build time appends
// " (built 3 days ago)"; a remind_after re-notification appends " (reminder)".
func ChangeText(event model.UpdateAvailable, escapeHTML bool) string {
	var s string
	switch {
	case event.LatestTag != "":
		left := escape(event.CurrentTag, escapeHTML)
		right := escape(event.LatestTag, escapeHTML)
		s = left + " → " + right
		if event.Bump == model.BumpMajor {
			s += " (major)"
		}
	case event.LatestVersion != "":
		latest := escape(event.LatestVersion, escapeHTML)
		switch event.CurrentVersion {
		case "":
			s = "new image " + latest
		case event.LatestVersion:
			s = latest + " rebuilt"
		default:
			s = escape(event.CurrentVersion, escapeHTML) + " → " + latest
			if event.Bump == model.BumpMajor {
				s += " (major)"
			}
		}
	default:
		s = "digest changed: " +
			escape(ShortDigest(event.LocalDigest), escapeHTML) +
			" → " +
			escape(ShortDigest(event.RemoteDigest), escapeHTML)
	}
	if built := BuiltAgo(event); built != "" {
		s += " (built " + built + ")"
	}
	if event.Reminder {
		s += " (reminder)"
	}
	return s
}

// BuiltAgo renders the new image's age relative to the check ("3 days ago");
// empty when the build time is unknown.
func BuiltAgo(event model.UpdateAvailable) string {
	if event.LatestCreated.IsZero() {
		return ""
	}
	ref := event.CheckedAt
	if ref.IsZero() {
		ref = time.Now()
	}
	age := ref.Sub(event.LatestCreated)
	switch {
	case age < time.Hour:
		return "less than an hour ago"
	case age < 2*time.Hour:
		return "1 hour ago"
	case age < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(age/time.Hour))
	default:
		return fmt.Sprintf("%d days ago", int(age/(24*time.Hour)))
	}
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
