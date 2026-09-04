package tool

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"

	"kite/email"
	"kite/store"
)

type EmailConfigProvider struct {
	DataDir string
	Domain  string
}

func (p *EmailConfigProvider) getConfig(userID string) (*store.EmailConfig, error) {
	ec, err := store.LoadEmailConfig(p.DataDir, userID)
	if err != nil || !ec.Enabled {
		return nil, fmt.Errorf("email not configured")
	}
	return ec, nil
}

type EmailSend struct {
	Provider EmailConfigProvider
}

func (t *EmailSend) Name() string        { return "email_send" }
func (t *EmailSend) Description() string { return "Send an email as the agent. Connects directly to destination MX servers." }
func (t *EmailSend) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"to":      strParam("Recipient email address."),
		"subject": strParam("Email subject line."),
		"body":    strParam("Plain text email body."),
	}, []string{"to", "subject", "body"})
}
func (t *EmailSend) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	ec, err := t.Provider.getConfig(userID)
	if err != nil {
		return err.Error(), nil
	}
	to := getStringArg(args, "to")
	subject := getStringArg(args, "subject")
	body := getStringArg(args, "body")

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		ec.Address, to, subject, body)

	domain := strings.SplitN(to, "@", 2)
	if len(domain) != 2 {
		return "Invalid recipient address", nil
	}
	mxs, err := net.LookupMX(domain[1])
	if err != nil || len(mxs) == 0 {
		return fmt.Sprintf("Cannot deliver to %s: no MX records found", domain[1]), nil
	}
	mx := strings.TrimSuffix(mxs[0].Host, ".") + ":25"

	c, err := smtp.Dial(mx)
	if err != nil {
		return fmt.Sprintf("SMTP connect error: %v", err), nil
	}
	defer c.Close()

	host, _, _ := net.SplitHostPort(mx)
	if err := c.Hello(host); err != nil {
		return fmt.Sprintf("HELO error: %v", err), nil
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: host}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Sprintf("STARTTLS error: %v", err), nil
		}
	}
	if err := c.Mail(ec.Address); err != nil {
		return fmt.Sprintf("MAIL FROM error: %v", err), nil
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Sprintf("RCPT TO error: %v", err), nil
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Sprintf("DATA error: %v", err), nil
	}
	w.Write([]byte(msg))
	w.Close()
	c.Quit()
	return fmt.Sprintf("Sent email to %s: %s", to, subject), nil
}

type EmailCheck struct {
	Maildir  *email.Maildir
	Provider EmailConfigProvider
}

func (t *EmailCheck) Name() string        { return "email_check" }
func (t *EmailCheck) Description() string { return "Check for new (unread) emails in the inbox. Returns a list of unread emails." }
func (t *EmailCheck) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"limit": intParam("Max emails to return (default 10)."),
	}, []string{})
}
func (t *EmailCheck) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	_, err := t.Provider.getConfig(userID)
	if err != nil {
		return err.Error(), nil
	}
	limit := getIntArg(args, "limit", 10)
	emails, err := t.Maildir.List(userID, true, limit)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if len(emails) == 0 {
		return "No unread emails.", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d unread email(s):\n", len(emails)))
	for _, e := range emails {
		sb.WriteString(fmt.Sprintf("  [%s] From: %s | Subject: %s\n", e.ID, e.From, e.Subject))
	}
	return sb.String(), nil
}

type EmailRead struct {
	Maildir  *email.Maildir
	Provider EmailConfigProvider
}

func (t *EmailRead) Name() string        { return "email_read" }
func (t *EmailRead) Description() string { return "Read a full email by its ID. Marks it as read." }
func (t *EmailRead) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"email_id": strParam("Email ID from email_check results."),
	}, []string{"email_id"})
}
func (t *EmailRead) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	_, err := t.Provider.getConfig(userID)
	if err != nil {
		return err.Error(), nil
	}
	id := getStringArg(args, "email_id")
	eml, err := t.Maildir.Get(userID, id)
	if err != nil {
		return fmt.Sprintf("Email %s not found.", id), nil
	}
	return fmt.Sprintf("From: %s\nSubject: %s\nDate: %s\nID: %s\n\n%s", eml.From, eml.Subject, eml.Date, eml.ID, eml.Body), nil
}

type EmailReply struct {
	Maildir  *email.Maildir
	Provider EmailConfigProvider
}

func (t *EmailReply) Name() string        { return "email_reply" }
func (t *EmailReply) Description() string { return "Reply to a received email. Includes proper threading headers." }
func (t *EmailReply) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"email_id": strParam("ID of the email to reply to."),
		"body":     strParam("Reply body text."),
	}, []string{"email_id", "body"})
}
func (t *EmailReply) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	ec, err := t.Provider.getConfig(userID)
	if err != nil {
		return err.Error(), nil
	}
	id := getStringArg(args, "email_id")
	body := getStringArg(args, "body")
	eml, err := t.Maildir.Get(userID, id)
	if err != nil {
		return fmt.Sprintf("Email %s not found.", id), nil
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: Re: %s\r\nIn-Reply-To: %s\r\nReferences: %s\r\n\r\n%s",
		ec.Address, eml.From, eml.Subject, eml.MessageID, eml.MessageID, body)

	send := EmailSend{Provider: t.Provider}
	return send.deliver(eml.From, msg)
}

type EmailForward struct {
	Maildir  *email.Maildir
	Provider EmailConfigProvider
}

func (t *EmailForward) Name() string        { return "email_forward" }
func (t *EmailForward) Description() string { return "Forward a received email to another address." }
func (t *EmailForward) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"email_id": strParam("ID of the email to forward."),
		"to":       strParam("Destination email address."),
	}, []string{"email_id", "to"})
}
func (t *EmailForward) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	ec, err := t.Provider.getConfig(userID)
	if err != nil {
		return err.Error(), nil
	}
	id := getStringArg(args, "email_id")
	to := getStringArg(args, "to")
	eml, err := t.Maildir.Get(userID, id)
	if err != nil {
		return fmt.Sprintf("Email %s not found.", id), nil
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: Fwd: %s\r\n\r\n---------- Forwarded ----------\r\nFrom: %s\r\nSubject: %s\r\n\r\n%s",
		ec.Address, to, eml.Subject, eml.From, eml.Subject, eml.Body)
	send := EmailSend{Provider: t.Provider}
	return send.deliver(to, msg)
}

type EmailDelete struct {
	Maildir  *email.Maildir
	Provider EmailConfigProvider
}

func (t *EmailDelete) Name() string        { return "email_delete" }
func (t *EmailDelete) Description() string { return "Delete an email from the inbox." }
func (t *EmailDelete) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"email_id": strParam("ID of the email to delete."),
	}, []string{"email_id"})
}
func (t *EmailDelete) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	_, err := t.Provider.getConfig(userID)
	if err != nil {
		return err.Error(), nil
	}
	id := getStringArg(args, "email_id")
	if err := t.Maildir.Delete(userID, id); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	return "Email deleted.", nil
}

type EmailSearch struct {
	Maildir  *email.Maildir
	Provider EmailConfigProvider
}

func (t *EmailSearch) Name() string        { return "email_search" }
func (t *EmailSearch) Description() string { return "Search emails in the inbox by subject, sender, or body content." }
func (t *EmailSearch) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"query": strParam("Search query to match against subject, from, and body."),
	}, []string{"query"})
}
func (t *EmailSearch) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	_, err := t.Provider.getConfig(userID)
	if err != nil {
		return err.Error(), nil
	}
	query := getStringArg(args, "query")
	emails, err := t.Maildir.Search(userID, query)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if len(emails) == 0 {
		return fmt.Sprintf("No emails matching '%s'.", query), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d email(s) found:\n", len(emails)))
	for _, e := range emails {
		sb.WriteString(fmt.Sprintf("  [%s] From: %s | Subject: %s\n", e.ID, e.From, e.Subject))
	}
	return sb.String(), nil
}

func (t *EmailSend) deliver(to, msg string) (string, error) {
	domain := strings.SplitN(to, "@", 2)
	if len(domain) != 2 {
		return "Invalid address", fmt.Errorf("bad address")
	}
	mxs, err := net.LookupMX(domain[1])
	if err != nil || len(mxs) == 0 {
		return fmt.Sprintf("Cannot deliver to %s: no MX records found", domain[1]), fmt.Errorf("no mx")
	}
	mx := strings.TrimSuffix(mxs[0].Host, ".") + ":25"

	c, err := smtp.Dial(mx)
	if err != nil {
		return fmt.Sprintf("SMTP connect error: %v", err), err
	}
	defer c.Close()
	host, _, _ := net.SplitHostPort(mx)
	if err := c.Hello(host); err != nil {
		return fmt.Sprintf("HELO error: %v", err), err
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: host}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Sprintf("STARTTLS error: %v", err), err
		}
	}
	from := extractFrom(msg)
	if from == "" {
		return "Could not extract From address from message", fmt.Errorf("no from")
	}
	if err := c.Mail(from); err != nil {
		return fmt.Sprintf("MAIL FROM error: %v", err), err
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Sprintf("RCPT TO error: %v", err), err
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Sprintf("DATA error: %v", err), err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Sprintf("Write error: %v", err), err
	}
	if err := w.Close(); err != nil {
		return fmt.Sprintf("Close error: %v", err), err
	}
	c.Quit()
	return "Sent.", nil
}

func extractFrom(msg string) string {
	lines := strings.Split(msg, "\r\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "from:") {
			addr := strings.TrimSpace(line[5:])
			if start := strings.Index(addr, "<"); start >= 0 {
				if end := strings.Index(addr[start:], ">"); end >= 0 {
					return addr[start+1 : start+end]
				}
			}
			return addr
		}
	}
	return ""
}
