package telegram

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/notifier/format"
)

const (
	defaultItemTemplate   = format.DefaultItemTemplate
	defaultDigestTemplate = format.DefaultDigestTemplate
)

func compileTemplate(name, body string) (*template.Template, error) {
	return template.New(name).Option("missingkey=zero").Parse(body)
}

func (n *Notifier) renderDigest(events []model.UpdateAvailable) (string, error) {
	items := make([]string, 0, len(events))
	for _, event := range events {
		item, err := n.renderItem(event)
		if err != nil {
			return "", err
		}
		if item != "" {
			items = append(items, item)
		}
	}

	var buf bytes.Buffer
	instance := format.ItemFromEvent(n.instanceName, model.UpdateAvailable{}, true).Instance
	err := n.digestTmpl.Execute(&buf, format.DigestData{
		Instance: instance,
		Count:    len(events),
		Items:    strings.Join(items, ""),
	})
	if err != nil {
		return "", fmt.Errorf("execute digest_template: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func (n *Notifier) renderItem(event model.UpdateAvailable) (string, error) {
	data := format.ItemFromEvent(n.instanceName, event, true)
	var buf bytes.Buffer
	if err := n.itemTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute item_template: %w", err)
	}
	return buf.String(), nil
}
