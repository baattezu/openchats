package provider

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type Capabilities struct {
	Groups    bool `json:"groups"`
	Media     bool `json:"media"`
	Voice     bool `json:"voice"`
	Edit      bool `json:"edit"`
	Delete    bool `json:"delete"`
	Templates bool `json:"templates"`
	History   bool `json:"history"`
}

func Caps(p string) Capabilities {
	switch p {
	case "whatsapp_web":
		return Capabilities{Groups: true, Media: true, Voice: true, Edit: true, Delete: true}
	case "telegram":
		return Capabilities{Groups: true, Media: true, Voice: true, Edit: true, Delete: true, History: true}
	case "whatsapp_cloud":
		return Capabilities{Media: true, Voice: true, Templates: true}
	case "instagram":
		return Capabilities{Media: true}
	default:
		return Capabilities{}
	}
}

type Connection struct {
	ID           string       `json:"id"`
	WorkspaceID  string       `json:"-"`
	Provider     string       `json:"provider"`
	Label        string       `json:"label"`
	Identity     string       `json:"identity"`
	Status       string       `json:"status"`
	Detail       string       `json:"detail"`
	ExternalID   string       `json:"-"`
	Capabilities Capabilities `json:"capabilities"`
}
type Media struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data []byte `json:"-"`
	ID   string `json:"id,omitempty"`
}
type Event struct {
	ExternalID string
	ChatID     string
	Title      string
	Group      bool
	Sender     string
	Direction  string
	Kind       string
	Text       string
	Media      *Media
	Status     string
	ReplyTo    string
	At         time.Time
	Edit       bool
	Delete     bool
	ReceiptIDs []string
	Metadata   json.RawMessage
}
type Send struct {
	ID         string          `json:"id"`
	ChatID     string          `json:"chat_id"`
	Text       string          `json:"text"`
	Kind       string          `json:"kind"`
	ReplyTo    string          `json:"reply_to"`
	Media      *Media          `json:"media,omitempty"`
	Template   json.RawMessage `json:"template,omitempty"`
	ExternalID string          `json:"external_id,omitempty"`
	Sender     string          `json:"sender,omitempty"`
}
type Callbacks struct {
	Event          func(context.Context, Event) error
	Status         func(string, string, string)
	SaveSession    func([]byte) error
	LoadSession    func() ([]byte, error)
	SaveCheckpoint func([]byte) error
	LoadCheckpoint func() ([]byte, error)
	// Halt останавливает сессию по решению провайдера (бан, сессию открыли в другом
	// месте, WhatsApp отвязал устройство) и НЕ даёт ей подняться самой: дальше только
	// ручное действие администратора. block — запрет новой привязки на этот срок,
	// UseRelinkCooldown — стандартный срок из конфигурации. wipe стирает сессию.
	Halt func(status, detail string, block time.Duration, wipe bool)
}

// UseRelinkCooldown просит Halt запретить новую привязку на стандартный срок
// WA_RELINK_COOLDOWN_HOURS.
const UseRelinkCooldown time.Duration = -1

type Adapter interface {
	Start(context.Context) error
	Send(context.Context, Send) (string, error)
	Edit(context.Context, Send) error
	Delete(context.Context, Send) error
	Disconnect(context.Context) error
	Challenge(context.Context, map[string]string) (map[string]any, error)
	Import(context.Context) error
}
type SendError struct {
	Err        error
	RetryAfter time.Duration
	Uncertain  bool
	Permanent  bool
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }

var ErrUnsupported = errors.New("operation is not supported by this connection")
