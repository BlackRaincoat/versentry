package discord

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestAllEmbedsHaveColor(t *testing.T) {
	const color = 5814783
	events := make([]model.UpdateAvailable, 12)
	for i := range events {
		events[i] = model.UpdateAvailable{
			Container:  model.Container{Name: fmt.Sprintf("svc_%d", i)},
			CurrentTag: "1.0.0",
			LatestTag:  "1.0.1",
		}
	}

	for _, e := range buildSimpleEmbeds(events, "prod", color) {
		if e.Color != color {
			t.Fatalf("simple embed color = %d, want %d", e.Color, color)
		}
	}

	longLine := strings.Repeat("x", 500)
	big := make([]model.UpdateAvailable, 0, 12)
	for i := 0; i < 12; i++ {
		big = append(big, model.UpdateAvailable{
			Container:  model.Container{Name: fmt.Sprintf("big_%d", i)},
			CurrentTag: longLine,
			LatestTag:  longLine + "2",
		})
	}
	for _, e := range buildDigestEmbeds(big, "prod", color) {
		if e.Color != color {
			t.Fatalf("digest embed color = %d, want %d", e.Color, color)
		}
	}
}

func TestPackEmbedsRespectsLimits(t *testing.T) {
	embeds := make([]Embed, 15)
	for i := range embeds {
		embeds[i] = Embed{
			Title:       fmt.Sprintf("title-%d", i),
			Description: "body",
			Color:       defaultEmbedColor,
		}
	}
	batches := packEmbedsIntoMessages(embeds)
	if len(batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(batches))
	}
	if len(batches[0]) != maxEmbedsPerMessage {
		t.Fatalf("first batch = %d, want %d", len(batches[0]), maxEmbedsPerMessage)
	}
}

func TestPayloadFromEmbedsSetsColorOnEach(t *testing.T) {
	embeds := []Embed{
		{Title: "a", Description: "one", Color: 123},
		{Title: "b", Description: "two", Color: 123},
	}
	body, err := payloadFromEmbeds(embeds, "bot")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Embeds []struct {
			Color int `json:"color"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Embeds) != 2 {
		t.Fatalf("embeds = %d", len(payload.Embeds))
	}
	for i, e := range payload.Embeds {
		if e.Color != 123 {
			t.Fatalf("embed[%d].color = %d", i, e.Color)
		}
	}
}

func TestSplitContent(t *testing.T) {
	chunks := splitContent(strings.Repeat("a", 2500), maxContentLen)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	if len(chunks[0]) != maxContentLen {
		t.Fatalf("first chunk len = %d", len(chunks[0]))
	}
}
