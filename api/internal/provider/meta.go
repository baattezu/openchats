package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"stepline/messaging-service/internal/secure"
	"strings"
	"time"
)

type Meta struct {
	Provider, ID, Token, Version string
	CB                           Callbacks
	Client                       *http.Client
	PublicMedia                  func(string) string
}

func (m *Meta) Start(ctx context.Context) error {
	if m.Token == "" || m.ID == "" {
		m.CB.Status("needs_auth", "Connect with Meta to grant access", "")
		<-ctx.Done()
		return nil
	}
	var v map[string]any
	if e := m.Request(ctx, "GET", m.ID+"?fields=id", nil, &v); e != nil {
		return e
	}
	m.CB.Status("connected", "Live webhook events; historical coverage is provider-dependent", m.ID)
	<-ctx.Done()
	return nil
}
func (m *Meta) base() string {
	if m.Provider == "instagram" {
		return "https://graph.instagram.com/" + m.Version + "/"
	}
	return "https://graph.facebook.com/" + m.Version + "/"
}
func (m *Meta) Request(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, m.base()+path, body)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+m.Token)
	if in != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return m.do(r, out)
}
func (m *Meta) RefreshInstagram(ctx context.Context, out any) error {
	q := url.Values{"grant_type": {"ig_refresh_token"}, "access_token": {m.Token}}
	r, e := http.NewRequestWithContext(ctx, "GET", "https://graph.instagram.com/refresh_access_token?"+q.Encode(), nil)
	if e != nil {
		return e
	}
	return m.do(r, out)
}
func (m *Meta) do(r *http.Request, out any) error {
	client := m.Client
	if client == nil {
		client = secure.PublicClient()
	}
	resp, e := client.Do(r)
	if e != nil {
		return &SendError{Err: errors.New("Meta request acceptance unknown"), Uncertain: r.Method != "GET", Permanent: r.Method == "GET"}
	}
	defer resp.Body.Close()
	b, e := secure.ReadLimited(resp.Body, 2<<20)
	if e != nil {
		return e
	}
	if resp.StatusCode == 429 {
		return &SendError{Err: errors.New("Meta rate limited"), RetryAfter: time.Minute}
	}
	if resp.StatusCode >= 500 {
		return &SendError{Err: errors.New("Meta server error; acceptance unknown"), Uncertain: r.Method != "GET", RetryAfter: time.Minute}
	}
	if resp.StatusCode >= 400 {
		var v struct {
			Error struct {
				Code    int `json:"code"`
				Subcode int `json:"error_subcode"`
			}
		}
		_ = json.Unmarshal(b, &v)
		return &SendError{Err: fmt.Errorf("Meta rejected request (HTTP %d, code %d, subcode %d)", resp.StatusCode, v.Error.Code, v.Error.Subcode), Permanent: true}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
func (m *Meta) Send(ctx context.Context, s Send) (string, error) {
	if m.Provider == "instagram" {
		message := map[string]any{"text": s.Text}
		if s.Media != nil {
			if m.PublicMedia == nil {
				return "", ErrUnsupported
			}
			kind := s.Kind
			if kind == "document" {
				kind = "file"
			}
			message = map[string]any{"attachment": map[string]any{"type": kind, "payload": map[string]string{"url": m.PublicMedia(s.Media.ID)}}}
			if s.Text != "" {
				return "", &SendError{Err: errors.New("Instagram attachment captions are unsupported; send text separately"), Permanent: true}
			}
		}
		var out struct {
			MessageID string `json:"message_id"`
		}
		e := m.Request(ctx, "POST", m.ID+"/messages", map[string]any{"recipient": map[string]string{"id": s.ChatID}, "message": message}, &out)
		return out.MessageID, e
	}
	payload := map[string]any{"messaging_product": "whatsapp", "to": s.ChatID, "type": "text", "text": map[string]any{"body": s.Text, "preview_url": false}}
	if len(s.Template) > 0 {
		payload["type"] = "template"
		delete(payload, "text")
		payload["template"] = s.Template
	}
	if s.Media != nil {
		id, e := m.upload(ctx, *s.Media)
		if e != nil {
			return "", e
		}
		kind := s.Kind
		if kind == "audio" {
			kind = "audio"
		}
		payload["type"] = kind
		delete(payload, "text")
		v := map[string]any{"id": id}
		if kind != "audio" {
			v["caption"] = s.Text
		}
		if kind == "document" {
			v["filename"] = s.Media.Name
		}
		payload[kind] = v
	}
	if s.ReplyTo != "" {
		payload["context"] = map[string]string{"message_id": s.ReplyTo}
	}
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if e := m.Request(ctx, "POST", m.ID+"/messages", payload, &out); e != nil {
		return "", e
	}
	if len(out.Messages) == 0 {
		return "", &SendError{Err: errors.New("Meta returned no message ID"), Uncertain: true}
	}
	return out.Messages[0].ID, nil
}
func (m *Meta) upload(ctx context.Context, media Media) (string, error) {
	var b bytes.Buffer
	f := multipart.NewWriter(&b)
	_ = f.WriteField("messaging_product", "whatsapp")
	_ = f.WriteField("type", media.MIME)
	p, e := f.CreateFormFile("file", media.Name)
	if e != nil {
		return "", e
	}
	_, _ = p.Write(media.Data)
	_ = f.Close()
	r, e := http.NewRequestWithContext(ctx, "POST", m.base()+m.ID+"/media", &b)
	if e != nil {
		return "", e
	}
	r.Header.Set("Authorization", "Bearer "+m.Token)
	r.Header.Set("Content-Type", f.FormDataContentType())
	var out struct {
		ID string `json:"id"`
	}
	e = m.do(r, &out)
	return out.ID, e
}
func (m *Meta) Download(ctx context.Context, id string) (*Media, error) {
	var v struct {
		URL  string `json:"url"`
		MIME string `json:"mime_type"`
		Size int64  `json:"file_size"`
	}
	if e := m.Request(ctx, "GET", url.PathEscape(id), nil, &v); e != nil {
		return nil, e
	}
	if v.Size > 25<<20 {
		return nil, errors.New("attachment exceeds limit")
	}
	u, e := url.Parse(v.URL)
	if e != nil || secure.ValidateURL(v.URL) != nil {
		return nil, errors.New("invalid Meta media URL")
	}
	host := u.Hostname()
	if !(host == "lookaside.fbsbx.com" || strings.HasSuffix(host, ".facebook.com") || strings.HasSuffix(host, ".fbcdn.net")) {
		return nil, errors.New("untrusted Meta media host")
	}
	r, e := http.NewRequestWithContext(ctx, "GET", v.URL, nil)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+m.Token)
	resp, e := secure.PublicClient().Do(r)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("media unavailable")
	}
	b, e := secure.ReadLimited(resp.Body, 25<<20)
	return &Media{Name: id, MIME: v.MIME, Data: b}, e
}
func (m *Meta) Edit(context.Context, Send) error   { return ErrUnsupported }
func (m *Meta) Delete(context.Context, Send) error { return ErrUnsupported }
func (m *Meta) Disconnect(context.Context) error   { return nil }
func (m *Meta) Challenge(context.Context, map[string]string) (map[string]any, error) {
	return map[string]any{"step": "oauth"}, nil
}
func (m *Meta) Import(context.Context) error { return ErrUnsupported }

type MetaEnvelope struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Value struct {
				Metadata struct {
					PhoneID string `json:"phone_number_id"`
				} `json:"metadata"`
				Contacts []struct {
					WaID    string `json:"wa_id"`
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
				} `json:"contacts"`
				Messages []struct {
					ID, From, Timestamp, Type string
					Text                      struct {
						Body string `json:"body"`
					} `json:"text"`
					Image, Document, Audio, Video *struct {
						ID       string `json:"id"`
						MIME     string `json:"mime_type"`
						Caption  string `json:"caption"`
						Filename string `json:"filename"`
					}
					Context struct {
						ID string `json:"id"`
					} `json:"context"`
				} `json:"messages"`
				Statuses []struct{ ID, Status string } `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
		Messaging []struct {
			Sender struct {
				ID string `json:"id"`
			} `json:"sender"`
			Recipient struct {
				ID string `json:"id"`
			} `json:"recipient"`
			Timestamp int64 `json:"timestamp"`
			Message   struct {
				ID          string `json:"mid"`
				Text        string `json:"text"`
				Echo        bool   `json:"is_echo"`
				Deleted     bool   `json:"is_deleted"`
				Attachments []struct {
					Type    string `json:"type"`
					Payload struct {
						URL string `json:"url"`
					} `json:"payload"`
				} `json:"attachments"`
			} `json:"message"`
			Read struct {
				Watermark int64 `json:"watermark"`
			} `json:"read"`
		} `json:"messaging"`
	} `json:"entry"`
}
