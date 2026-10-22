// Package notify implements the Notifier port: SMTP email, HTTP push
// (ntfy-compatible) and a structured-log fallback, plus a fan-out.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/ipenas-cl/leafwise/internal/ports"
)

// Email sends plain-text mail through any SMTP relay (Mailpit in demo mode).
type Email struct {
	Addr string // host:port
	From string
	Auth smtp.Auth
	send func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

func (e *Email) Name() string { return "email" }

func (e *Email) Notify(_ context.Context, n ports.Notification) error {
	msg := fmt.Sprintf("From: Leafwise <%s>\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		e.From, n.To, n.Subject, time.Now().Format(time.RFC1123Z), n.Body)
	send := e.send
	if send == nil {
		send = smtp.SendMail
	}
	return send(e.Addr, e.Auth, e.From, []string{n.To}, []byte(msg))
}

// Push publishes to an ntfy-compatible topic (https://ntfy.sh or self-hosted),
// which fans out to the ntfy mobile app and browser push.
type Push struct {
	URL   string // e.g. https://ntfy.sh/leafwise-<random>
	Token string
	HTTP  *http.Client
}

func (p *Push) Name() string { return "push" }

func (p *Push) Notify(ctx context.Context, n ports.Notification) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, strings.NewReader(n.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", n.Subject)
	req.Header.Set("Tags", "seedling")
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	c := p.HTTP
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("push: status %d", resp.StatusCode)
	}
	return nil
}

// Log writes notifications to the structured log. Useful in development.
type Log struct{ L *slog.Logger }

func (l Log) Name() string { return "log" }
func (l Log) Notify(ctx context.Context, n ports.Notification) error {
	l.L.InfoContext(ctx, "notification", "to", n.To, "subject", n.Subject)
	return nil
}

// Fanout delivers to every channel; it succeeds if at least one channel did.
type Fanout []ports.Notifier

func (f Fanout) Name() string { return "fanout" }

func (f Fanout) Notify(ctx context.Context, n ports.Notification) error {
	var errs []error
	ok := 0
	for _, c := range f {
		if err := c.Notify(ctx, n); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name(), err))
		} else {
			ok++
		}
	}
	if ok == 0 && len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
