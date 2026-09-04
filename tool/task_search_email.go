package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kite/email"
)

type TaskSearchEmail struct {
	DataDir string
	Maildir *email.Maildir
}

func (t *TaskSearchEmail) Name() string        { return "task_search_email" }
func (t *TaskSearchEmail) Description() string { return "Search email with automatic query expansion. Fans out to multiple time ranges and keywords for comprehensive results." }
func (t *TaskSearchEmail) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"query": strParam("Natural language search query."),
		"limit": intParam("Max results (default 10)."),
	}, []string{"query"})
}
func (t *TaskSearchEmail) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	query := getStringArg(args, "query")
	limit := getIntArg(args, "limit", 10)
	if query == "" {
		return "Error: query is required", nil
	}

	// Fan out to multiple time-based searches
	now := time.Now()
	timeQueries := []string{
		query,
		query + " " + now.Format("January 2006"),
		query + " " + fmt.Sprintf("%d", now.Year()),
	}

	seen := make(map[string]bool)
	var results []email.Email

	for _, q := range timeQueries {
		matches, err := t.Maildir.Search(userID, q)
		if err != nil {
			continue
		}
		for _, m := range matches {
			if seen[m.MessageID] {
				continue
			}
			seen[m.MessageID] = true
			results = append(results, m)
		}
		if len(results) >= limit {
			break
		}
	}

	if len(results) == 0 {
		return fmt.Sprintf("No emails found for '%s'.", query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Emails matching '%s' (%d results):\n", query, len(results)))
	for i, e := range results {
		if i >= limit {
			break
		}
		status := " "
		if !e.Read {
			status = "●"
		}
		sb.WriteString(fmt.Sprintf("%s %s | %s | %s\n   %s\n", status, e.From, e.Subject, e.Date, previewBody([]byte(e.Body), 120)))
	}
	return sb.String(), nil
}
