package mail

import (
	"strings"
	"testing"
)

func TestFormatBlocksHeaderInjection(t *testing.T) {
	out := string(format("App <app@example.com>", "a@example.com", Message{
		Subject: "Hello\r\nBcc: victim@example.com",
		Body:    "line one\nline two",
	}))
	head, body, _ := strings.Cut(out, "\r\n\r\n")
	if strings.Contains(head, "\r\nBcc:") {
		t.Fatalf("header injection possible:\n%s", head)
	}
	if body != "line one\r\nline two" {
		t.Fatalf("body = %q, want CRLF line endings", body)
	}
}

func TestMemoryLast(t *testing.T) {
	m := &Memory{}
	_ = m.Send(t.Context(), Message{To: "a@example.com", Subject: "1"})
	_ = m.Send(t.Context(), Message{To: "b@example.com", Subject: "2"})
	_ = m.Send(t.Context(), Message{To: "A@example.com", Subject: "3"})
	if got, ok := m.Last("a@example.com"); !ok || got.Subject != "3" {
		t.Fatalf("Last = %+v, %v", got, ok)
	}
	if _, ok := m.Last("c@example.com"); ok {
		t.Fatal("found a message that was never sent")
	}
}
