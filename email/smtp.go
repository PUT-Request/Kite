package email

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"kite/store"
)

type SMTPServer struct {
	port     string
	domain   string
	dataDir  string
	maildir  *Maildir
	onEmail  func(userID string, eml *Email)
}

func NewSMTPServer(port int, domain, dataDir string, md *Maildir) *SMTPServer {
	return &SMTPServer{
		port:    fmt.Sprintf(":%d", port),
		domain:  domain,
		dataDir: dataDir,
		maildir: md,
	}
}

func (s *SMTPServer) SetOnEmail(fn func(userID string, eml *Email)) {
	s.onEmail = fn
}

func (s *SMTPServer) Start() {
	ln, err := net.Listen("tcp", s.port)
	if err != nil {
		log.Printf("SMTP server error: %v", err)
		return
	}
	log.Printf("SMTP server listening on %s", s.port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go s.handle(conn)
	}
}

func (s *SMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Minute))
	r := bufio.NewReader(conn)
	write := func(code int, msg string) {
		fmt.Fprintf(conn, "%d %s\r\n", code, msg)
	}

	write(220, s.domain+" ESMTP Kite")

	var from, rawData string
	var recipients []string

	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "HELO") || strings.HasPrefix(upper, "EHLO"):
			write(250, "Hello")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			from = extractAddr(line)
			write(250, "OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpt := extractAddr(line)
			if !strings.HasSuffix(rcpt, "@"+s.domain) {
				write(550, "Relaying denied")
				continue
			}
			if userID, _ := store.UserByEmail(s.dataDir, s.domain, rcpt); userID == "" {
				write(550, "User not found")
				continue
			}
			recipients = append(recipients, rcpt)
			write(250, "OK")
		case upper == "DATA":
			write(354, "End data with <CR><LF>.<CR><LF>")
			rawData, err = readData(r)
			if err != nil {
				return
			}
			write(250, "OK queued")
		case upper == "QUIT":
			write(221, "Bye")
			conn.Close()
			if rawData != "" && len(recipients) > 0 {
				s.deliver(from, recipients, rawData)
			}
			return
		default:
			write(500, "Unknown command")
		}
	}
}

func (s *SMTPServer) deliver(from string, recipients []string, rawData string) {
	eml := parseEmail(from, recipients, rawData)
	for _, rcpt := range recipients {
		userID, _ := store.UserByEmail(s.dataDir, s.domain, rcpt)
		if userID == "" {
			continue
		}
		emlCopy := *eml
		emlCopy.To = []string{rcpt}
		if err := s.maildir.Add(userID, &emlCopy); err != nil {
			log.Printf("Failed to store email for %s: %v", userID, err)
			continue
		}
		log.Printf("Email delivered to %s: %s", userID, emlCopy.Subject)
		if s.onEmail != nil {
			go s.onEmail(userID, &emlCopy)
		}
	}
}

func parseEmail(from string, recipients []string, raw string) *Email {
	eml := &Email{
		From:      from,
		To:        recipients,
		Date:      time.Now().UTC().Format(time.RFC3339),
		MessageID: fmt.Sprintf("<%d@kite>", time.Now().UnixNano()),
	}

	lines := strings.Split(raw, "\r\n")
	headerDone := false
	for _, line := range lines {
		if !headerDone {
			if line == "" {
				headerDone = true
				continue
			}
			lower := strings.ToLower(line)
			switch {
			case strings.HasPrefix(lower, "subject:"):
				eml.Subject = strings.TrimSpace(line[8:])
			case strings.HasPrefix(lower, "date:"):
				eml.Date = strings.TrimSpace(line[5:])
			case strings.HasPrefix(lower, "message-id:"):
				eml.MessageID = strings.TrimSpace(line[11:])
			case strings.HasPrefix(lower, "in-reply-to:"):
				eml.ThreadID = strings.TrimSpace(line[12:])
			}
		} else {
			if line == "." {
				break
			}
			eml.Body += line + "\n"
		}
	}
	eml.Body = strings.TrimSpace(eml.Body)
	return eml
}

func extractAddr(line string) string {
	start := strings.Index(line, "<")
	end := strings.LastIndex(line, ">")
	if start >= 0 && end > start {
		return line[start+1 : end]
	}
	parts := strings.Split(line, ":")
	if len(parts) > 1 {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(line)
}

func readData(r *bufio.Reader) (string, error) {
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return data.String(), fmt.Errorf("connection closed before end of data")
			}
			return "", err
		}
		if line == ".\r\n" || line == ".\n" {
			break
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		data.WriteString(line)
		if data.Len() > 50*1024*1024 {
			return "", fmt.Errorf("message too large (max 50MB)")
		}
	}
	return data.String(), nil
}
