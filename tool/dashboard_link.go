package tool

import (
	"context"
	"fmt"
)

type DashboardLink struct {
	Generate      func(userID, channelID string) string
	ChannelLookup func(userID string) (string, bool)
}

func (t *DashboardLink) Name() string        { return "generate_dashboard_link" }
func (t *DashboardLink) Description() string { return "Generate an ephemeral dashboard link for the user. Use this when the user wants to manage integrations (Notion, GitHub, Dropbox), view settings, or connect new services. The link expires in 5 minutes." }
func (t *DashboardLink) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"reason": strParam("Brief reason for generating the link (e.g., 'connect Notion', 'manage settings')."),
	}, []string{})
}

func (t *DashboardLink) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	if t.Generate == nil {
		return "Dashboard isn't set up on this server.", nil
	}
	channelID := ""
	if t.ChannelLookup != nil {
		if ch, ok := t.ChannelLookup(userID); ok {
			channelID = ch
		}
	}
	link := t.Generate(userID, channelID)
	return fmt.Sprintf("Dashboard link: %s\n\nIt works for 5 minutes. User can connect Notion, GitHub, Dropbox from there.", link), nil
}

var globalDashboardGen func(userID, channelID string) string

func SetDashboardGenerator(fn func(userID, channelID string) string) {
	globalDashboardGen = fn
}

func GenerateDashboardLink(userID, channelID string) string {
	if globalDashboardGen == nil {
		return "dashboard not available"
	}
	return globalDashboardGen(userID, channelID)
}
