// Package mail sends the portal e-mails (license confirmation, removal
// notices, decisions). Two transports: SMTP (Google Workspace SMTP relay in
// production) and "log" (development: the message is written to the logs).
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"mime"
	"net/smtp"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed templates/*
var templatesFS embed.FS

// Message is a multipart (text + HTML) e-mail.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer sends messages.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// --- Log transport -----------------------------------------------------------

// LogMailer writes messages to the logs (development).
type LogMailer struct{}

// Send implements Mailer.
func (LogMailer) Send(_ context.Context, m Message) error {
	slog.Info("e-mail (log transport)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}

// --- SMTP transport ------------------------------------------------------------

// SMTPMailer sends through an SMTP relay (STARTTLS negotiated automatically).
type SMTPMailer struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// Send implements Mailer. net/smtp has no context support, so the call is run
// in a goroutine and abandoned if ctx expires.
func (s SMTPMailer) Send(ctx context.Context, m Message) error {
	raw, err := build(s.From, m)
	if err != nil {
		return err
	}
	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Password, s.Host)
	}
	done := make(chan error, 1)
	go func() {
		done <- smtp.SendMail(s.Host+":"+strconv.Itoa(s.Port), auth, s.From, []string{m.To}, raw)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// build renders a RFC 5322 multipart/alternative message.
func build(from string, m Message) ([]byte, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	boundary := "gcalm-" + hex.EncodeToString(b)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", m.To)
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	buf.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	for _, part := range []struct{ ctype, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		fmt.Fprintf(&buf, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n", boundary, part.ctype)
		buf.WriteString(strings.ReplaceAll(part.body, "\n", "\r\n"))
		buf.WriteString("\r\n")
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes(), nil
}

// --- Templates -----------------------------------------------------------------

// Renderer renders the e-mail templates (templates/<name>.txt and .html).
type Renderer struct {
	text *texttemplate.Template
	html *htmltemplate.Template
}

// NewRenderer parses the embedded templates.
func NewRenderer(loc *time.Location) (*Renderer, error) {
	funcs := map[string]any{
		"date": func(t any) string {
			switch v := t.(type) {
			case time.Time:
				return v.In(loc).Format("02 Jan 2006")
			case *time.Time:
				if v != nil {
					return v.In(loc).Format("02 Jan 2006")
				}
			}
			return "—"
		},
	}
	text, err := texttemplate.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.txt")
	if err != nil {
		return nil, err
	}
	html, err := htmltemplate.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Renderer{text: text, html: html}, nil
}

// Render builds a Message from the template pair "name".
func (r *Renderer) Render(name, to, subject string, data any) (Message, error) {
	var t, h bytes.Buffer
	if err := r.text.ExecuteTemplate(&t, name+".txt", data); err != nil {
		return Message{}, err
	}
	if err := r.html.ExecuteTemplate(&h, name+".html", data); err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: subject, Text: t.String(), HTML: h.String()}, nil
}
