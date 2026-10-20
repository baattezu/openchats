package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMetaTextWireAndUncertainFailures(t *testing.T) {
	var body map[string]any
	m := &Meta{Provider: "whatsapp_cloud", ID: "123", Token: "test-token", Version: "v25.0", Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v25.0/123/messages" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("wrong Meta endpoint/auth")
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"messages":[{"id":"wamid.fixture"}]}`))}, nil
	})}}
	id, e := m.Send(context.Background(), Send{ChatID: "15550000000", Text: "hello"})
	if e != nil || id != "wamid.fixture" || body["messaging_product"] != "whatsapp" {
		t.Fatal("wrong send", id, e, body)
	}
	m.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("transport broke") })
	_, e = m.Send(context.Background(), Send{ChatID: "15550000000", Text: "hello"})
	var se *SendError
	if !errors.As(e, &se) || !se.Uncertain {
		t.Fatal("ambiguous failure would be retried")
	}
}
func TestInstagramAttachmentUsesSignedURL(t *testing.T) {
	m := &Meta{Provider: "instagram", ID: "456", Token: "test-token", Version: "v25.0", PublicMedia: func(id string) string { return "https://example.com/signed/" + id }, Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var b map[string]any
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Fatal(e)
		}
		message := b["message"].(map[string]any)
		payload := message["attachment"].(map[string]any)["payload"].(map[string]any)
		if payload["url"] != "https://example.com/signed/media" {
			t.Fatal("wrong attachment URL")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"message_id":"ig.fixture"}`))}, nil
	})}}
	id, e := m.Send(context.Background(), Send{ChatID: "789", Kind: "image", Media: &Media{ID: "media"}})
	if e != nil || id != "ig.fixture" {
		t.Fatal(id, e)
	}
}
