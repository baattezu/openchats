package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"strings"
	"sync"
	"time"
)

type WhatsApp struct {
	Client    *whatsmeow.Client
	CB        Callbacks
	mu        sync.Mutex
	qr        string
	titles    map[string]string
	container *sqlstore.Container
}

func NewWhatsApp(ctx context.Context, container *sqlstore.Container, jid string, cb Callbacks) (*WhatsApp, error) {
	var w WhatsApp
	w.CB = cb
	w.container = container
	w.titles = map[string]string{}
	d := container.NewDevice()
	if jid != "" {
		j, e := types.ParseJID(jid)
		if e != nil {
			return nil, e
		}
		d, e = container.GetDevice(ctx, j)
		if e != nil {
			return nil, e
		}
		if d == nil {
			return nil, errors.New("saved WhatsApp device missing; pair again")
		}
	}
	w.Client = whatsmeow.NewClient(d, nil)
	w.Client.AddEventHandler(w.handle)
	return &w, nil
}
func (w *WhatsApp) Start(ctx context.Context) error {
	if w.Client.Store.ID == nil {
		ch, e := w.Client.GetQRChannel(ctx)
		if e != nil {
			return e
		}
		go func() {
			for x := range ch {
				w.mu.Lock()
				if x.Event == "code" {
					b, e := qrcode.Encode(x.Code, qrcode.Medium, 256)
					if e == nil {
						w.qr = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
					}
				} else {
					w.qr = ""
				}
				w.mu.Unlock()
				if x.Event == "code" {
					w.CB.Status("pairing", "Scan with WhatsApp → Linked devices", "")
				} else if x.Event == "timeout" {
					w.CB.Status("needs_auth", "QR expired; reconnect to pair", "")
				}
			}
		}()
	}
	if e := w.Client.Connect(); e != nil {
		return e
	}
	<-ctx.Done()
	w.Client.Disconnect()
	return nil
}
func (w *WhatsApp) handle(raw any) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	switch e := raw.(type) {
	case *events.Connected:
		w.mu.Lock()
		w.qr = ""
		w.mu.Unlock()
		jid := w.Client.Store.GetJID().String()
		if err := w.CB.SaveSession([]byte(jid)); err != nil {
			w.CB.Status("error", "Session persistence failed", "")
			w.Client.Disconnect()
			return
		}
		w.CB.Status("connected", "History coverage: live events and provider-delivered sync", jid)
	case *events.Disconnected:
		w.CB.Status("reconnecting", "Connection interrupted; waiting for library reconnect", "")
	case *events.LoggedOut:
		_ = w.CB.SaveSession(nil)
		w.CB.Status("needs_auth", "Linked device was logged out; pair again", "")
	case *events.Receipt:
		status := "delivered"
		if string(e.Type) == "read" || string(e.Type) == "read-self" {
			status = "read"
		}
		if err := w.CB.Event(ctx, Event{ReceiptIDs: e.MessageIDs, Status: status}); err != nil {
			w.CB.Status("error", "Could not persist receipt", "")
		}
	case *events.Message:
		if err := w.message(ctx, e); err != nil {
			w.CB.Status("error", "Message persistence failed; inspect service health", "")
		}
	case *events.HistorySync:
		for _, c := range e.Data.GetConversations() {
			chat, err := types.ParseJID(c.GetID())
			if err != nil {
				continue
			}
			for _, m := range c.GetMessages() {
				if evt, err := w.Client.ParseWebMessage(chat, m.GetMessage()); err == nil {
					if err = w.message(ctx, evt); err != nil {
						return
					}
				}
			}
		}
	}
}
func (w *WhatsApp) message(ctx context.Context, e *events.Message) error {
	m := e.Message
	v := Event{ExternalID: e.Info.ID, ChatID: e.Info.Chat.String(), Title: e.Info.PushName, Group: e.Info.IsGroup, Sender: e.Info.Sender.String(), At: e.Info.Timestamp, Direction: "in", Kind: "text", Text: m.GetConversation(), Edit: e.IsEdit}
	if e.Info.IsFromMe {
		v.Direction = "out"
		v.Status = "sent"
	}
	if e.IsViewOnce || e.IsEphemeral {
		return nil
	}
	if x := m.GetProtocolMessage(); x != nil && x.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT {
		v.ExternalID = x.GetKey().GetID()
		v.Edit = true
		if x.GetTimestampMS() > 0 {
			v.At = time.UnixMilli(x.GetTimestampMS())
		}
		m = x.GetEditedMessage()
		v.Text = m.GetConversation()
	}
	if e.Info.IsGroup && !v.Edit {
		w.mu.Lock()
		title := w.titles[v.ChatID]
		w.mu.Unlock()
		if title == "" {
			if info, err := w.Client.GetGroupInfo(ctx, e.Info.Chat); err == nil {
				title = info.Name
				w.mu.Lock()
				w.titles[v.ChatID] = title
				w.mu.Unlock()
			}
		}
		v.Title = title
	}
	if x := m.GetExtendedTextMessage(); x != nil {
		v.Text = x.GetText()
		v.ReplyTo = x.GetContextInfo().GetStanzaID()
	}
	if x := m.GetProtocolMessage(); x != nil && x.GetType() == waE2E.ProtocolMessage_REVOKE {
		v.ExternalID = x.GetKey().GetID()
		v.Delete = true
	}
	var name, mime string
	var size uint64
	switch {
	case m.GetImageMessage() != nil:
		x := m.GetImageMessage()
		v.Kind = "image"
		v.Text = x.GetCaption()
		mime = x.GetMimetype()
		size = x.GetFileLength()
		name = "image.jpg"
	case m.GetDocumentMessage() != nil:
		x := m.GetDocumentMessage()
		v.Kind = "document"
		v.Text = x.GetCaption()
		mime = x.GetMimetype()
		name = x.GetFileName()
		size = x.GetFileLength()
	case m.GetAudioMessage() != nil:
		x := m.GetAudioMessage()
		v.Kind = "audio"
		mime = x.GetMimetype()
		name = "audio.ogg"
		size = x.GetFileLength()
	case m.GetVideoMessage() != nil:
		x := m.GetVideoMessage()
		v.Kind = "video"
		v.Text = x.GetCaption()
		mime = x.GetMimetype()
		name = "video.mp4"
		size = x.GetFileLength()
	case v.Text == "" && !v.Delete:
		v.Kind = "unsupported"
		v.Text = "Unsupported WhatsApp message"
	}
	if mime != "" {
		if size > 25<<20 {
			v.Kind = "unsupported"
			v.Text = "Attachment exceeds 25 MiB"
		} else {
			b, err := w.Client.DownloadAny(ctx, m)
			if err != nil {
				v.Text += " [attachment unavailable]"
			} else {
				v.Media = &Media{Name: name, MIME: mime, Data: b}
			}
		}
	}
	return w.CB.Event(ctx, v)
}
func (w *WhatsApp) Send(ctx context.Context, s Send) (string, error) {
	to, e := types.ParseJID(s.ChatID)
	if e != nil {
		return "", e
	}
	m := &waE2E.Message{Conversation: proto.String(s.Text)}
	if s.Media != nil {
		mt := whatsmeow.MediaDocument
		if s.Kind == "image" {
			mt = whatsmeow.MediaImage
		}
		if s.Kind == "audio" {
			mt = whatsmeow.MediaAudio
		}
		if s.Kind == "video" {
			mt = whatsmeow.MediaVideo
		}
		up, e := w.Client.Upload(ctx, s.Media.Data, mt)
		if e != nil {
			return "", &SendError{Err: errors.New("WhatsApp media upload failed"), Permanent: true}
		}
		switch s.Kind {
		case "image":
			m = &waE2E.Message{ImageMessage: &waE2E.ImageMessage{URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength, Mimetype: &s.Media.MIME, Caption: &s.Text}}
		case "audio":
			m = &waE2E.Message{AudioMessage: &waE2E.AudioMessage{URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength, Mimetype: &s.Media.MIME, PTT: proto.Bool(true)}}
		case "video":
			m = &waE2E.Message{VideoMessage: &waE2E.VideoMessage{URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength, Mimetype: &s.Media.MIME, Caption: &s.Text}}
		default:
			m = &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength, Mimetype: &s.Media.MIME, FileName: &s.Media.Name, Caption: &s.Text}}
		}
	}
	resp, e := w.Client.SendMessage(ctx, to, m, whatsmeow.SendRequestExtra{ID: strings.ReplaceAll(s.ID, "-", "")})
	if e != nil {
		return "", &SendError{Err: errors.New("WhatsApp send acceptance unknown"), Uncertain: true}
	}
	return resp.ID, nil
}
func (w *WhatsApp) Edit(ctx context.Context, s Send) error {
	j, e := types.ParseJID(s.ChatID)
	if e != nil {
		return e
	}
	_, e = w.Client.SendMessage(ctx, j, w.Client.BuildEdit(j, s.ExternalID, &waE2E.Message{Conversation: proto.String(s.Text)}))
	return e
}
func (w *WhatsApp) Delete(ctx context.Context, s Send) error {
	j, e := types.ParseJID(s.ChatID)
	if e != nil {
		return e
	}
	_, e = w.Client.RevokeMessage(ctx, j, s.ExternalID)
	return e
}
func (w *WhatsApp) Disconnect(ctx context.Context) error {
	if w.Client.Store.ID == nil {
		w.Client.Disconnect()
		return nil
	}
	e := w.Client.Logout(ctx)
	w.Client.Disconnect()
	return e
}
func (w *WhatsApp) Challenge(ctx context.Context, in map[string]string) (map[string]any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.qr == "" {
		return map[string]any{"step": "waiting", "detail": "Waiting for pairing QR or connection"}, nil
	}
	return map[string]any{"step": "qr", "qr": w.qr}, nil
}
func (w *WhatsApp) Import(context.Context) error {
	return fmt.Errorf("WhatsApp history arrives during pairing; manual import is unavailable")
}
