package notify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"testing"

	"github.com/ipenas-cl/leafwise/internal/ports"
)

var n = ports.Notification{To: "me@example.com", Subject: "Water Basil today", Body: "deficit reached"}

func TestEmailFormatsMessage(t *testing.T) {
	var got string
	e := &Email{Addr: "mail:1025", From: "bot@leafwise.local", send: func(addr string, _ smtp.Auth, from string, to []string, msg []byte) error {
		got = string(msg)
		if addr != "mail:1025" || to[0] != "me@example.com" {
			t.Errorf("bad envelope %s %v", addr, to)
		}
		return nil
	}}
	if err := e.Notify(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Subject: Water Basil today\r\n") || !strings.HasSuffix(got, "deficit reached\r\n") {
		t.Fatalf("bad message:\n%s", got)
	}
}

func TestPushPostsToTopic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Title") != n.Subject || r.Header.Get("Authorization") != "Bearer t" {
			t.Error("missing headers")
		}
	}))
	defer srv.Close()
	if err := (&Push{URL: srv.URL + "/leafwise", Token: "t"}).Notify(context.Background(), n); err != nil {
		t.Fatal(err)
	}
}

type failing struct{}

func (failing) Name() string                                     { return "fail" }
func (failing) Notify(context.Context, ports.Notification) error { return errors.New("down") }

func TestFanoutToleratesPartialFailure(t *testing.T) {
	ok := &Email{send: func(string, smtp.Auth, string, []string, []byte) error { return nil }}
	if err := (Fanout{failing{}, ok}).Notify(context.Background(), n); err != nil {
		t.Fatalf("one healthy channel is enough: %v", err)
	}
	if err := (Fanout{failing{}, failing{}}).Notify(context.Background(), n); err == nil {
		t.Fatal("all channels failing must return an error")
	}
}
