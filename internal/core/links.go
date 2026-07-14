package core

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/BlackRaincoat/versentry/internal/imageweb"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
)

// LinkRow is one line of versentry links output.
type LinkRow struct {
	Container string
	ImageTag  string
	Mode      string
	URL       string
}

// Links writes a table of notification URLs for all monitored containers.
// Does not contact registries, update state, or send notifications.
// Image OCI labels come from ListRunning (already merged with container labels).
func (a *App) Links(ctx context.Context, w io.Writer) error {
	return a.engine.WriteLinks(ctx, w)
}

// WriteLinks lists monitored containers and prints container / image:tag / mode / url.
func (e *Engine) WriteLinks(ctx context.Context, w io.Writer) error {
	listCtx, cancel := context.WithTimeout(ctx, e.timeouts.Provider.Duration)
	defer cancel()

	containers, err := e.provider.ListRunning(listCtx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	monitored, excluded := filterByWatch(containers, e.log)
	e.log.Info("listed running containers",
		"count", len(containers),
		"monitored", len(monitored),
		"excluded", excluded,
	)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CONTAINER\tIMAGE:TAG\tMODE\tURL")

	for _, c := range monitored {
		row := linkRowFor(e, c)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.Container, row.ImageTag, row.Mode, row.URL)
	}

	return tw.Flush()
}

func linkRowFor(e *Engine, c model.Container) LinkRow {
	name := c.Name
	if name == "" {
		name = "-"
	}
	imageTag := c.ImageRef
	if imageTag == "" {
		imageTag = "-"
	}

	parsed, err := imageref.Parse(c.ImageRef)
	if err != nil {
		return LinkRow{
			Container: name,
			ImageTag:  imageTag,
			Mode:      "error",
			URL:       fmt.Sprintf("parse image ref: %v", err),
		}
	}
	if parsed.Tag == "" {
		return LinkRow{
			Container: name,
			ImageTag:  imageTag,
			Mode:      "error",
			URL:       "digest-only reference",
		}
	}

	mode, _ := resolveTrackingMode(e.rules, e.log, parsed.Host, parsed.Repo, parsed.Tag, c.Labels)
	link := imageweb.URL(parsed.Host, parsed.Repo, parsed.Tag, c.Labels, mode)
	if link == "" {
		link = "(no url)"
	}
	return LinkRow{
		Container: name,
		ImageTag:  imageTag,
		Mode:      mode,
		URL:       link,
	}
}
