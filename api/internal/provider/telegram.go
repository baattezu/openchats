package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tgSession struct{ cb Callbacks }

func (s tgSession) LoadSession(context.Context) ([]byte, error) {
	b, e := s.cb.LoadSession()
	if e == nil && len(b) == 0 {
		return nil, session.ErrNotFound
	}
	return b, e
}
func (s tgSession) StoreSession(_ context.Context, b []byte) error { return s.cb.SaveSession(b) }

type tgCheckpoint struct {
	State       updates.State
	Found       bool
	Channels    map[int64]int
	ChannelHash map[int64]int64
	UserHash    map[int64]int64
	Names       map[string]string
	Self        int64
}
type Telegram struct {
	Client          *telegram.Client
	CB              Callbacks
	mu              sync.Mutex
	authMu          sync.Mutex
	cp              tgCheckpoint
	ready, authed   chan struct{}
	once            sync.Once
	phone, codeHash string
	manager         *updates.Manager
}

func NewTelegram(id int, hash string, cb Callbacks) (*Telegram, error) {
	t := &Telegram{CB: cb, ready: make(chan struct{}), authed: make(chan struct{}), cp: tgCheckpoint{Channels: map[int64]int{}, ChannelHash: map[int64]int64{}, UserHash: map[int64]int64{}, Names: map[string]string{}}}
	b, e := cb.LoadCheckpoint()
	if e != nil {
		return nil, e
	}
	if len(b) > 0 {
		if e = json.Unmarshal(b, &t.cp); e != nil {
			return nil, e
		}
	}
	d := tg.NewUpdateDispatcher()
	d.OnNewMessage(func(ctx context.Context, en tg.Entities, u *tg.UpdateNewMessage) error {
		return t.consume(ctx, en, u.Message, false)
	})
	d.OnNewChannelMessage(func(ctx context.Context, en tg.Entities, u *tg.UpdateNewChannelMessage) error {
		return t.consume(ctx, en, u.Message, false)
	})
	d.OnEditMessage(func(ctx context.Context, en tg.Entities, u *tg.UpdateEditMessage) error {
		return t.consume(ctx, en, u.Message, true)
	})
	d.OnEditChannelMessage(func(ctx context.Context, en tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return t.consume(ctx, en, u.Message, true)
	})
	d.OnDeleteMessages(func(ctx context.Context, en tg.Entities, u *tg.UpdateDeleteMessages) error {
		for _, id := range u.Messages {
			if e := cb.Event(ctx, Event{ExternalID: fmt.Sprintf("m:%d", id), Delete: true}); e != nil {
				return e
			}
		}
		return nil
	})
	d.OnDeleteChannelMessages(func(ctx context.Context, en tg.Entities, u *tg.UpdateDeleteChannelMessages) error {
		for _, id := range u.Messages {
			if e := cb.Event(ctx, Event{ExternalID: fmt.Sprintf("c:%d:%d", u.ChannelID, id), Delete: true}); e != nil {
				return e
			}
		}
		return nil
	})
	t.manager = updates.New(updates.Config{Handler: d, Storage: t, AccessHasher: t, UserAccessHasher: t, MaxChannelDifferenceConcurrency: 1, OnTooLong: func() { cb.Status("connected", "History gap detected; use bounded import", "") }, OnChannelTooLong: func(int64) { cb.Status("connected", "Group history gap detected; use bounded import", "") }})
	t.Client = telegram.NewClient(id, hash, telegram.Options{SessionStorage: tgSession{cb}, UpdateHandler: t.manager})
	return t, nil
}
func (t *Telegram) Start(ctx context.Context) error {
	return t.Client.Run(ctx, func(ctx context.Context) error {
		close(t.ready)
		s, e := t.Client.Auth().Status(ctx)
		if e != nil {
			return e
		}
		if !s.Authorized {
			t.CB.Status("needs_auth", "Enter your phone number to sign in", "")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.authed:
			}
		}
		me, e := t.Client.Self(ctx)
		if e != nil {
			return e
		}
		t.mu.Lock()
		t.cp.Self = me.ID
		e = t.persist()
		t.mu.Unlock()
		if e != nil {
			return e
		}
		identity := me.Phone
		if me.Username != "" {
			identity = "@" + me.Username
		}
		t.CB.Status("connected", "Live cloud messages; optional import: 50 chats × 100 messages", identity)
		return t.manager.Run(ctx, t.Client.API(), me.ID, updates.AuthOptions{})
	})
}
func (t *Telegram) Challenge(ctx context.Context, in map[string]string) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.ready:
	}
	t.authMu.Lock()
	defer t.authMu.Unlock()
	status, err := t.Client.Auth().Status(ctx)
	if err != nil {
		return nil, tgError(err, false)
	}
	if status.Authorized {
		return map[string]any{"step": "connected"}, nil
	}
	if in["phone"] != "" {
		v, e := t.Client.Auth().SendCode(ctx, in["phone"], auth.SendCodeOptions{})
		if e != nil {
			return nil, tgError(e, false)
		}
		switch x := v.(type) {
		case *tg.AuthSentCode:
			t.phone = in["phone"]
			t.codeHash = x.PhoneCodeHash
			return map[string]any{"step": "code"}, nil
		case *tg.AuthSentCodeSuccess:
			t.once.Do(func() { close(t.authed) })
			return map[string]any{"step": "connected"}, nil
		}
		return nil, errors.New("unsupported Telegram login challenge")
	}
	if in["code"] != "" {
		_, e := t.Client.Auth().SignIn(ctx, t.phone, in["code"], t.codeHash)
		if errors.Is(e, auth.ErrPasswordAuthNeeded) {
			return map[string]any{"step": "password"}, nil
		}
		if e != nil {
			return nil, tgError(e, false)
		}
		t.once.Do(func() { close(t.authed) })
		return map[string]any{"step": "connected"}, nil
	}
	if in["password"] != "" {
		_, e := t.Client.Auth().Password(ctx, in["password"])
		if e != nil {
			return nil, tgError(e, false)
		}
		t.once.Do(func() { close(t.authed) })
		return map[string]any{"step": "connected"}, nil
	}
	return map[string]any{"step": "phone"}, nil
}
func tgError(e error, send bool) error {
	if e == nil {
		return nil
	}
	if d, ok := tgerr.AsFloodWait(e); ok {
		return &SendError{Err: errors.New("Telegram requested cooldown"), RetryAfter: d}
	}
	var rpc *tgerr.Error
	if errors.As(e, &rpc) {
		return &SendError{Err: fmt.Errorf("Telegram rejected request (%s)", rpc.Type), Permanent: true}
	}
	return &SendError{Err: errors.New("Telegram transport failed"), Uncertain: send, Permanent: !send}
}
func tgChat(p tg.PeerClass) string {
	switch p := p.(type) {
	case *tg.PeerUser:
		return fmt.Sprintf("user:%d", p.UserID)
	case *tg.PeerChat:
		return fmt.Sprintf("chat:%d", p.ChatID)
	case *tg.PeerChannel:
		return fmt.Sprintf("channel:%d", p.ChannelID)
	}
	return ""
}
func tgExternal(p tg.PeerClass, id int) string {
	if c, ok := p.(*tg.PeerChannel); ok {
		return fmt.Sprintf("c:%d:%d", c.ChannelID, id)
	}
	return fmt.Sprintf("m:%d", id)
}
func (t *Telegram) cache(en tg.Entities) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, u := range en.Users {
		t.cp.UserHash[id] = u.AccessHash
		t.cp.Names[fmt.Sprintf("user:%d", id)] = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}
	for id, c := range en.Chats {
		t.cp.Names[fmt.Sprintf("chat:%d", id)] = c.Title
	}
	for id, c := range en.Channels {
		t.cp.ChannelHash[id] = c.AccessHash
		t.cp.Names[fmt.Sprintf("channel:%d", id)] = c.Title
	}
	return t.persist()
}
func (t *Telegram) consume(ctx context.Context, en tg.Entities, raw tg.MessageClass, edit bool) error {
	m, ok := raw.(*tg.Message)
	if !ok {
		return nil
	}
	if e := t.cache(en); e != nil {
		return e
	}
	chat := tgChat(m.PeerID)
	if chat == "" {
		return nil
	}
	t.mu.Lock()
	title := t.cp.Names[chat]
	self := t.cp.Self
	t.mu.Unlock()
	direction := "in"
	status := "received"
	if m.Out {
		direction = "out"
		status = "sent"
	}
	v := Event{ExternalID: tgExternal(m.PeerID, m.ID), ChatID: chat, Title: title, Group: !strings.HasPrefix(chat, "user:"), Sender: tgChat(m.FromID), Direction: direction, Status: status, Text: m.Message, Kind: "text", At: time.Unix(int64(m.Date), 0), Edit: edit}
	if edit && m.EditDate > 0 {
		v.At = time.Unix(int64(m.EditDate), 0)
	}
	if m.FromID == nil && m.Out {
		v.Sender = fmt.Sprintf("user:%d", self)
	}
	if _, ok := m.Media.(*tg.MessageMediaUnsupported); ok {
		v.Kind = "unsupported"
	}
	if !edit && m.Media != nil {
		media, kind, e := t.download(ctx, m.Media)
		if kind != "" {
			v.Kind = kind
		}
		if e != nil {
			v.Text += " [attachment unavailable]"
		} else {
			v.Media = media
		}
	}
	return t.CB.Event(ctx, v)
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 25<<20 {
		return 0, errors.New("attachment exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (t *Telegram) download(ctx context.Context, raw tg.MessageMediaClass) (*Media, string, error) {
	var location tg.InputFileLocationClass
	var mime, name, kind string
	switch x := raw.(type) {
	case *tg.MessageMediaDocument:
		d, ok := x.Document.(*tg.Document)
		if !ok {
			return nil, "unsupported", nil
		}
		if d.Size > 25<<20 {
			return nil, "unsupported", nil
		}
		location = &tg.InputDocumentFileLocation{ID: d.ID, AccessHash: d.AccessHash, FileReference: d.FileReference}
		mime = d.MimeType
		name = "document"
		kind = "document"
		for _, attr := range d.Attributes {
			switch a := attr.(type) {
			case *tg.DocumentAttributeFilename:
				name = a.FileName
			case *tg.DocumentAttributeAudio:
				kind = "audio"
			case *tg.DocumentAttributeVideo:
				kind = "video"
			}
		}
	case *tg.MessageMediaPhoto:
		p, ok := x.Photo.(*tg.Photo)
		if !ok {
			return nil, "unsupported", nil
		}
		size := ""
		for _, s := range p.Sizes {
			if x, ok := s.(*tg.PhotoSize); ok {
				size = x.Type
			}
		}
		if size == "" {
			return nil, "unsupported", nil
		}
		location = &tg.InputPhotoFileLocation{ID: p.ID, AccessHash: p.AccessHash, FileReference: p.FileReference, ThumbSize: size}
		mime = "image/jpeg"
		name = "photo.jpg"
		kind = "image"
	default:
		return nil, "unsupported", nil
	}
	var b boundedBuffer
	_, e := downloader.NewDownloader().Download(t.Client.API(), location).Stream(ctx, &b)
	return &Media{Name: name, MIME: mime, Data: b.Bytes()}, kind, e
}
func (t *Telegram) peer(chat string) (tg.InputPeerClass, error) {
	parts := strings.Split(chat, ":")
	if len(parts) != 2 {
		return nil, errors.New("invalid Telegram peer")
	}
	id, e := strconv.ParseInt(parts[1], 10, 64)
	if e != nil {
		return nil, e
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch parts[0] {
	case "user":
		h, ok := t.cp.UserHash[id]
		if !ok {
			return nil, errors.New("Telegram peer unknown; import dialogs first")
		}
		return &tg.InputPeerUser{UserID: id, AccessHash: h}, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: id}, nil
	case "channel":
		h, ok := t.cp.ChannelHash[id]
		if !ok {
			return nil, errors.New("Telegram group unknown")
		}
		return &tg.InputPeerChannel{ChannelID: id, AccessHash: h}, nil
	}
	return nil, errors.New("invalid Telegram peer")
}
func (t *Telegram) Send(ctx context.Context, s Send) (string, error) {
	peer, e := t.peer(s.ChatID)
	if e != nil {
		return "", &SendError{Err: e, Permanent: true}
	}
	h := sha256.Sum256([]byte(s.ID))
	random := int64(binary.LittleEndian.Uint64(h[:8]))
	var u tg.UpdatesClass
	if s.Media == nil {
		u, e = t.Client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: s.Text, RandomID: random})
	} else {
		var f tg.InputFileClass
		f, e = uploader.NewUploader(t.Client.API()).FromBytes(ctx, s.Media.Name, s.Media.Data)
		if e != nil {
			return "", tgError(e, false)
		}
		var media tg.InputMediaClass
		if s.Kind == "image" {
			media = &tg.InputMediaUploadedPhoto{File: f}
		} else {
			attrs := []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: s.Media.Name}}
			if s.Kind == "audio" {
				attrs = append(attrs, &tg.DocumentAttributeAudio{Voice: true})
			}
			media = &tg.InputMediaUploadedDocument{File: f, MimeType: s.Media.MIME, Attributes: attrs}
		}
		u, e = t.Client.API().MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: peer, Media: media, Message: s.Text, RandomID: random})
	}
	if e != nil {
		return "", tgError(e, true)
	}
	id := 0
	switch x := u.(type) {
	case *tg.UpdateShortSentMessage:
		id = x.ID
	case *tg.Updates:
		for _, up := range x.Updates {
			if x, ok := up.(*tg.UpdateMessageID); ok && x.RandomID == random {
				id = x.ID
			}
		}
	case *tg.UpdatesCombined:
		for _, up := range x.Updates {
			if x, ok := up.(*tg.UpdateMessageID); ok && x.RandomID == random {
				id = x.ID
			}
		}
	}
	if id == 0 {
		return "", &SendError{Err: errors.New("Telegram accepted without identifiable message ID"), Uncertain: true}
	}
	if strings.HasPrefix(s.ChatID, "channel:") {
		return "c:" + strings.TrimPrefix(s.ChatID, "channel:") + fmt.Sprintf(":%d", id), nil
	}
	return fmt.Sprintf("m:%d", id), nil
}
func tgMessageID(s string) (int, error) { p := strings.Split(s, ":"); return strconv.Atoi(p[len(p)-1]) }
func (t *Telegram) Edit(ctx context.Context, s Send) error {
	p, e := t.peer(s.ChatID)
	if e != nil {
		return e
	}
	id, e := tgMessageID(s.ExternalID)
	if e != nil {
		return e
	}
	_, e = t.Client.API().MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{Peer: p, ID: id, Message: s.Text})
	return tgError(e, true)
}
func (t *Telegram) Delete(ctx context.Context, s Send) error {
	id, e := tgMessageID(s.ExternalID)
	if e != nil {
		return e
	}
	if strings.HasPrefix(s.ChatID, "channel:") {
		p, e := t.peer(s.ChatID)
		if e != nil {
			return e
		}
		c := p.(*tg.InputPeerChannel)
		_, e = t.Client.API().ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{Channel: &tg.InputChannel{ChannelID: c.ChannelID, AccessHash: c.AccessHash}, ID: []int{id}})
		return tgError(e, true)
	}
	_, e = t.Client.API().MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{Revoke: true, ID: []int{id}})
	return tgError(e, true)
}
func (t *Telegram) Disconnect(ctx context.Context) error {
	_, e := t.Client.API().AuthLogOut(ctx)
	return tgError(e, false)
}
func (t *Telegram) Import(ctx context.Context) error {
	raw, e := t.Client.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 50})
	if e != nil {
		return tgError(e, false)
	}
	d, ok := raw.AsModified()
	if !ok {
		return nil
	}
	en := tg.Entities{Users: map[int64]*tg.User{}, Chats: map[int64]*tg.Chat{}, Channels: map[int64]*tg.Channel{}}
	for _, u := range d.GetUsers() {
		if x, ok := u.(*tg.User); ok {
			en.Users[x.ID] = x
		}
	}
	for _, c := range d.GetChats() {
		switch x := c.(type) {
		case *tg.Chat:
			en.Chats[x.ID] = x
		case *tg.Channel:
			en.Channels[x.ID] = x
		}
	}
	if e = t.cache(en); e != nil {
		return e
	}
	for _, raw := range d.GetDialogs() {
		x, ok := raw.(*tg.Dialog)
		if !ok {
			continue
		}
		peer, e := t.peer(tgChat(x.Peer))
		if e != nil {
			return e
		}
		v, e := t.Client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: 100})
		if e != nil {
			return tgError(e, false)
		}
		v2, ok := v.AsModified()
		if !ok {
			continue
		}
		msgs := v2.GetMessages()
		for i := len(msgs) - 1; i >= 0; i-- {
			if e = t.consume(ctx, en, msgs[i], false); e != nil {
				return e
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	t.CB.Status("connected", "Imported up to 50 conversations × 100 messages; older history excluded", "")
	return nil
}
func (t *Telegram) persist() error {
	b, e := json.Marshal(t.cp)
	if e != nil {
		return e
	}
	return t.CB.SaveCheckpoint(b)
}
func (t *Telegram) GetState(_ context.Context, _ int64) (updates.State, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cp.State, t.cp.Found, nil
}
func (t *Telegram) SetState(_ context.Context, _ int64, s updates.State) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cp.State = s
	t.cp.Found = true
	return t.persist()
}
func (t *Telegram) update(fn func(*updates.State)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.cp.State)
	return t.persist()
}
func (t *Telegram) SetPts(_ context.Context, _ int64, v int) error {
	return t.update(func(s *updates.State) { s.Pts = v })
}
func (t *Telegram) SetQts(_ context.Context, _ int64, v int) error {
	return t.update(func(s *updates.State) { s.Qts = v })
}
func (t *Telegram) SetDate(_ context.Context, _ int64, v int) error {
	return t.update(func(s *updates.State) { s.Date = v })
}
func (t *Telegram) SetSeq(_ context.Context, _ int64, v int) error {
	return t.update(func(s *updates.State) { s.Seq = v })
}
func (t *Telegram) SetDateSeq(_ context.Context, _ int64, d, s int) error {
	return t.update(func(v *updates.State) { v.Date = d; v.Seq = s })
}
func (t *Telegram) GetChannelPts(_ context.Context, _, id int64) (int, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.cp.Channels[id]
	return v, ok, nil
}
func (t *Telegram) SetChannelPts(_ context.Context, _, id int64, v int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cp.Channels[id] = v
	return t.persist()
}
func (t *Telegram) ForEachChannels(ctx context.Context, _ int64, f func(context.Context, int64, int) error) error {
	t.mu.Lock()
	cp := map[int64]int{}
	for k, v := range t.cp.Channels {
		cp[k] = v
	}
	t.mu.Unlock()
	for k, v := range cp {
		if e := f(ctx, k, v); e != nil {
			return e
		}
	}
	return nil
}
func (t *Telegram) SetChannelAccessHash(_ context.Context, _, id, hash int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cp.ChannelHash[id] = hash
	return t.persist()
}
func (t *Telegram) GetChannelAccessHash(_ context.Context, _, id int64) (int64, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.cp.ChannelHash[id]
	return v, ok, nil
}
func (t *Telegram) SetUserAccessHash(_ context.Context, _, id, hash int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cp.UserHash[id] = hash
	return t.persist()
}
func (t *Telegram) GetUserAccessHash(_ context.Context, _, id int64) (int64, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.cp.UserHash[id]
	return v, ok, nil
}
