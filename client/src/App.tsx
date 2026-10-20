import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type FormEvent,
} from "react";
import {
  ArrowLeft,
  ArrowUpRight,
  Camera,
  Check,
  CheckCheck,
  ChevronRight,
  Copy,
  KeyRound,
  LogOut,
  MessageCircle,
  MessagesSquare,
  Paperclip,
  Pencil,
  Plus,
  RefreshCw,
  Send,
  Settings,
  Trash2,
  Users,
  X,
} from "lucide-react";
import {
  api,
  ApiError,
  labels,
  type Connection,
  type Conversation,
  type Me,
  type Message,
  type Media,
  type Provider,
} from "./api";
import { embeddedSignup } from "./meta";

function ChannelIcon({ provider }: { provider: Provider }) {
  return (
    <span
      className={
        "channel-icon " +
        (provider.startsWith("whatsapp") ? "whatsapp" : provider)
      }
    >
      {provider === "telegram" ? (
        <Send />
      ) : provider === "instagram" ? (
        <Camera />
      ) : (
        <MessageCircle />
      )}
    </span>
  );
}
function Dialog({
  title,
  children,
  onClose,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
    return () => ref.current?.close();
  }, []);
  return (
    <dialog
      ref={ref}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      aria-labelledby="dialog-title"
    >
      <div className="modal-head">
        <h2 id="dialog-title">{title}</h2>
        <button className="icon" onClick={onClose} aria-label="Close dialog">
          <X />
        </button>
      </div>
      <div className="modal-body">{children}</div>
    </dialog>
  );
}
function Empty({ title, text }: { title: string; text: string }) {
  return (
    <div className="empty">
      <MessagesSquare />
      <h3>{title}</h3>
      <p>{text}</p>
    </div>
  );
}
const time = (s: string) =>
  new Date(s).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
function Login({ done }: { done: () => void }) {
  const invitation = new URLSearchParams(location.hash.slice(1)).get("invite");
  const [email, setEmail] = useState(""),
    [password, setPassword] = useState(""),
    [name, setName] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api(
        invitation ? "/auth/accept-invitation" : "/auth/login",
        "POST",
        invitation
          ? { token: invitation, password, name }
          : { email, password },
      );
      history.replaceState(null, "", location.pathname);
      done();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="shell">
      <section className="login">
        <div className="brand">
          <MessagesSquare />
          Messaging Service
        </div>
        <h1>{invitation ? "Join your workspace" : "Welcome back"}</h1>
        <p>
          {invitation
            ? "Create your account to connect with the team."
            : "Sign in to your team’s connected inbox."}
        </p>
        <form className="form" onSubmit={submit}>
          {invitation ? (
            <label className="field">
              Your name
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="name"
                required
              />
            </label>
          ) : (
            <label className="field">
              Email
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="username"
                required
              />
            </label>
          )}
          <label className="field">
            Password
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete={invitation ? "new-password" : "current-password"}
              minLength={invitation ? 12 : undefined}
              required
            />
          </label>
          {error && (
            <div className="notice error-notice" role="alert">
              {error}
            </div>
          )}
          <button className="primary" disabled={busy}>
            {busy ? "Signing in…" : invitation ? "Join workspace" : "Sign in"}
            <ArrowUpRight />
          </button>
        </form>
      </section>
    </main>
  );
}

export function App() {
  const [me, setMe] = useState<Me | null>(null),
    [boot, setBoot] = useState(true),
    [connections, setConnections] = useState<Connection[]>([]),
    [conversations, setConversations] = useState<Conversation[]>([]),
    [selected, setSelected] = useState<Conversation | null>(null),
    [messages, setMessages] = useState<Message[]>([]),
    [modal, setModal] = useState<"connect" | "settings" | null>(null),
    [editingConnection, setEditingConnection] = useState<Connection | null>(
      null,
    ),
    [toast, setToast] = useState(""),
    [query, setQuery] = useState(""),
    [filter, setFilter] = useState(""),
    [refreshing, setRefreshing] = useState(false),
    [allChats, setAllChats] = useState(false);
  const chatBody = useRef<HTMLDivElement>(null),
    follow = useRef(true),
    olderScroll = useRef<{ height: number; top: number } | null>(null);
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const version = useRef(0);
  async function refresh() {
    try {
      const [cs, vs] = await Promise.all([
        api<Connection[]>("/connections"),
        api<Conversation[]>("/conversations"),
      ]);
      setConnections(cs);
      setConversations(vs);
      setAllChats(vs.length < 100);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setMe(null);
      else setToast((e as Error).message);
    }
  }
  async function auth() {
    setBoot(true);
    try {
      const m = await api<Me>("/me");
      setMe(m);
      await refresh();
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 401))
        setToast((e as Error).message);
    } finally {
      setBoot(false);
    }
  }
  async function loadMessages(c: Conversation, older = false) {
    const current = ++version.current;
    try {
      const before = older && messages.length ? messages[0].id : "";
      if (older && chatBody.current) {
        olderScroll.current = {
          height: chatBody.current.scrollHeight,
          top: chatBody.current.scrollTop,
        };
        follow.current = false;
      }
      const ms = await api<Message[]>(
        `/conversations/${c.id}/messages${before ? "?before=" + before : ""}`,
      );
      if (selectedRef.current?.id !== c.id || current !== version.current)
        return;
      setMessages((old) => (older ? [...ms.reverse(), ...old] : ms.reverse()));
    } catch (e) {
      setToast((e as Error).message);
    }
  }
  useEffect(() => {
    void auth();
  }, []);
  useEffect(() => {
    if (!me) return;
    const events = new EventSource("/api/v1/events");
    let timer: ReturnType<typeof setTimeout> | undefined;
    events.onmessage = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        void refresh();
        const c = selectedRef.current;
        if (c) void loadMessages(c);
      }, 180);
    };
    events.onopen = () => {
      void refresh();
      const c = selectedRef.current;
      if (c) void loadMessages(c);
    };
    const poll = setInterval(() => {
      void refresh();
      const c = selectedRef.current;
      if (c) void loadMessages(c);
    }, 15_000);
    return () => {
      events.close();
      clearInterval(poll);
      clearTimeout(timer);
    };
  }, [me?.id]);
  useEffect(() => {
    setMessages([]);
    follow.current = true;
    olderScroll.current = null;
    if (selected && window.innerWidth < 768)
      document
        .querySelector<HTMLButtonElement>(".back")
        ?.focus({ preventScroll: true });
    if (!selected) {
      return;
    }
    void loadMessages(selected);
    void api(`/conversations/${selected.id}/read`, "POST", {})
      .then(() =>
        setConversations((cs) =>
          cs.map((c) => (c.id === selected.id ? { ...c, unread: 0 } : c)),
        ),
      )
      .catch((e) => setToast(e.message));
  }, [selected?.id]);
  useLayoutEffect(() => {
    const body = chatBody.current;
    if (!body) return;
    if (olderScroll.current) {
      body.scrollTop =
        olderScroll.current.top +
        body.scrollHeight -
        olderScroll.current.height;
      olderScroll.current = null;
    } else if (follow.current) body.scrollTop = body.scrollHeight;
  }, [messages]);
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(""), 8000);
    return () => clearTimeout(t);
  }, [toast]);
  if (boot)
    return (
      <main className="shell">
        <div className="login">
          <div className="brand">
            <MessagesSquare />
            Messaging Service
          </div>
          <p className="loading">Opening your workspace…</p>
        </div>
      </main>
    );
  if (!me)
    return (
      <>
        <Login done={() => void auth()} />
        {toast && (
          <div className="toast" role="alert">
            {toast}
            <button
              onClick={() => setToast("")}
              aria-label="Dismiss notification"
            >
              <X size={16} />
            </button>
          </div>
        )}
      </>
    );
  const conn = connections.find((c) => c.id === selected?.connection_id);
  const shown = conversations.filter(
    (c) =>
      (!filter || c.connection_id === filter) &&
      (c.title + " " + c.preview).toLowerCase().includes(query.toLowerCase()),
  );
  return (
    <main className="shell">
      <header className="brandbar">
        <div className="brand">
          <MessagesSquare />
          Messaging Service
        </div>
        <small>One workspace. Every conversation.</small>
      </header>
      <div className="workspace">
        <div className="left">
          <section className="panel identity" aria-label="Your workspace">
            <div className="avatar">
              {(me.name || me.email).slice(0, 2).toUpperCase()}
            </div>
            <div className="identity-copy">
              <strong>{me.name || me.email}</strong>
              <small>Internal workspace · {me.role}</small>
            </div>
            <button
              className="icon"
              aria-label="Refresh inbox"
              disabled={refreshing}
              onClick={async () => {
                setRefreshing(true);
                await refresh();
                if (selected) await loadMessages(selected);
                setRefreshing(false);
              }}
            >
              <RefreshCw className={refreshing ? "loading" : ""} />
            </button>
            {me.role === "admin" && (
              <button
                className="key-control"
                onClick={() => setModal("settings")}
              >
                <KeyRound />
                <code>API & workspace</code>
              </button>
            )}
            <button
              className="icon"
              aria-label="Sign out"
              onClick={() =>
                void api("/auth/logout", "POST", {})
                  .then(() => {
                    setMe(null);
                    setSelected(null);
                  })
                  .catch((e) => setToast(e.message))
              }
            >
              <LogOut />
            </button>
          </section>
          <section className="panel" aria-label="Connected accounts">
            <div className="section-head">
              <div className="heading">
                <h2>Channels</h2>
                <span className="count">{connections.length}</span>
              </div>
              {me.role === "admin" && (
                <button
                  onClick={() => {
                    setEditingConnection(null);
                    setModal("connect");
                  }}
                >
                  <Plus />
                  Connect
                </button>
              )}
            </div>
            {connections.length === 0 ? (
              <Empty
                title="Connect your first channel"
                text="Add WhatsApp, Instagram, or Telegram. Your team can reply from here."
              />
            ) : (
              connections.map((c) => (
                <button
                  className="channel-row"
                  key={c.id}
                  disabled={me.role !== "admin"}
                  onClick={() => {
                    setEditingConnection(c);
                    setModal("connect");
                  }}
                >
                  <ChannelIcon provider={c.provider} />
                  <span className="channel-copy">
                    <strong>{c.label}</strong>
                    <small>{c.identity || labels[c.provider]}</small>
                  </span>
                  <span className={"status " + c.status}>
                    {c.status.replace("_", " ")}
                  </span>
                  <ChevronRight size={14} />
                </button>
              ))
            )}
          </section>
          <section className="panel inbox" aria-label="Message feed">
            <div className="section-head">
              <div className="heading">
                <h2>Messages</h2>
                <span className="count">
                  {conversations.reduce((n, c) => n + Number(c.unread), 0)}{" "}
                  unread
                </span>
              </div>
              <small>Latest conversations</small>
            </div>
            <div className="filter">
              <input
                aria-label="Search conversations"
                placeholder="Search conversations"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <select
                aria-label="Filter account"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
              >
                <option value="">All channels</option>
                {connections.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.label}
                  </option>
                ))}
              </select>
            </div>
            {shown.length === 0 ? (
              <Empty
                title={
                  conversations.length
                    ? "No matching conversations"
                    : "Your inbox is ready"
                }
                text={
                  conversations.length
                    ? "Try another name or channel."
                    : "New messages appear here once a channel is connected."
                }
              />
            ) : (
              shown.map((c) => (
                <button
                  className={
                    "conversation " + (selected?.id === c.id ? "selected" : "")
                  }
                  key={c.id}
                  onClick={() => setSelected(c)}
                >
                  <ChannelIcon provider={c.provider} />
                  <span className="conversation-copy">
                    <span className="conversation-title">
                      <strong>{c.title}</strong>
                      {c.is_group && <Users size={12} />}
                    </span>
                    <p>{c.preview || "Attachment"}</p>
                  </span>
                  <span className="conversation-meta">
                    <span>{time(c.last_message_at)}</span>
                    {Number(c.unread) > 0 ? (
                      <span className="unread">{c.unread}</span>
                    ) : (
                      <span>Reply</span>
                    )}
                  </span>
                </button>
              ))
            )}
            {!allChats && (
              <button
                className="history-button"
                onClick={async () => {
                  try {
                    const next = await api<Conversation[]>(
                      "/conversations?before=" + conversations.at(-1)!.id,
                    );
                    setConversations((cs) => [...cs, ...next]);
                    setAllChats(next.length < 100);
                  } catch (e) {
                    setToast((e as Error).message);
                  }
                }}
              >
                Older conversations
              </button>
            )}
          </section>
        </div>
        <section
          className={"panel chat " + (selected ? "active" : "")}
          aria-label="Conversation"
        >
          {selected ? (
            <>
              <div className="chat-head">
                <button
                  className="icon back"
                  onClick={() => setSelected(null)}
                  aria-label="Back to messages"
                >
                  <ArrowLeft />
                </button>
                <ChannelIcon provider={selected.provider} />
                <div className="heading">
                  <h2>{selected.title}</h2>
                  <small>
                    {selected.account_label} ·{" "}
                    {selected.is_group ? "Group" : labels[selected.provider]}
                  </small>
                </div>
                <span className={"status " + conn?.status}>
                  {conn?.status || "unavailable"}
                </span>
              </div>
              <div
                className="chat-body"
                ref={chatBody}
                onScroll={() => {
                  const b = chatBody.current;
                  if (b)
                    follow.current =
                      b.scrollHeight - b.scrollTop - b.clientHeight < 80;
                }}
              >
                {messages.length >= 100 && (
                  <button
                    className="history-button"
                    onClick={() => void loadMessages(selected, true)}
                  >
                    Load earlier messages
                  </button>
                )}
                {messages.length === 0 ? (
                  <Empty
                    title="Loading conversation"
                    text="Messages stored for this account appear here."
                  />
                ) : (
                  messages.map((m) => (
                    <MessageBubble
                      key={m.id}
                      message={m}
                      connection={conn}
                      notice={setToast}
                      changed={() => void loadMessages(selected)}
                    />
                  ))
                )}
              </div>
              <Composer
                key={selected.id}
                conversation={selected}
                connection={conn}
                stop={me.config.stop_all_sends}
                notice={setToast}
                changed={() => {
                  void loadMessages(selected);
                  void refresh();
                }}
              />
            </>
          ) : (
            <Empty
              title="A little room to talk"
              text="Select a conversation to read messages and send a reply."
            />
          )}
        </section>
      </div>
      <footer className="footer">
        <span>Messaging Service · v1</span>
        <span>
          {me.config.stop_all_sends ? "Sending paused" : "Internal workspace"}
        </span>
      </footer>
      {modal === "connect" && (
        <ConnectDialog
          initial={
            editingConnection
              ? connections.find((c) => c.id === editingConnection.id) ||
                editingConnection
              : null
          }
          me={me}
          close={() => {
            setModal(null);
            setEditingConnection(null);
            void refresh();
          }}
          notice={setToast}
          changed={() => void refresh()}
        />
      )}
      {modal === "settings" && (
        <WorkspaceDialog close={() => setModal(null)} notice={setToast} />
      )}
      {toast && (
        <div className="toast" role="status">
          {toast}
          <button
            onClick={() => setToast("")}
            aria-label="Dismiss notification"
          >
            <X size={16} />
          </button>
        </div>
      )}
    </main>
  );
}

function MessageBubble({
  message: m,
  connection: c,
  notice,
  changed,
}: {
  message: Message;
  connection?: Connection;
  notice: (s: string) => void;
  changed: () => void;
}) {
  const [editing, setEditing] = useState(false),
    [text, setText] = useState(m.text),
    [busy, setBusy] = useState(false);
  async function action(remove = false) {
    setBusy(true);
    try {
      await api(
        "/messages/" + m.id,
        remove ? "DELETE" : "PATCH",
        remove ? undefined : { text },
      );
      setEditing(false);
      changed();
    } catch (e) {
      notice((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const actionable =
    m.direction === "out" &&
    !!m.external_id &&
    ["sent", "delivered", "read"].includes(m.status) &&
    !m.deleted_at &&
    c?.status === "connected";
  return (
    <article className={"bubble " + m.direction}>
      {m.direction === "in" && m.sender && (
        <small className="sender">{m.sender}</small>
      )}
      {m.deleted_at ? (
        <p className="muted">Message deleted</p>
      ) : (
        <>
          {editing ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void action();
              }}
            >
              <textarea
                aria-label="Edit message text"
                value={text}
                onChange={(e) => setText(e.target.value)}
              />
              <div className="row">
                <button disabled={busy}>Save</button>
                <button type="button" onClick={() => setEditing(false)}>
                  Cancel
                </button>
              </div>
            </form>
          ) : (
            m.text && <p>{m.text}</p>
          )}
          {m.media && (
            <>
              <a
                className="attachment"
                href={"/api/v1/media/" + m.media.id}
                target="_blank"
                rel="noreferrer"
              >
                <Paperclip size={14} />
                {m.media.name}
              </a>
              {m.media.mime.startsWith("audio/") && (
                <audio
                  controls
                  preload="none"
                  src={"/api/v1/media/" + m.media.id}
                  style={{ maxWidth: "100%" }}
                />
              )}
            </>
          )}
          {m.kind === "unsupported" && !m.text && (
            <p className="muted">Unsupported message type</p>
          )}
        </>
      )}
      <div className="bubble-foot">
        <small>
          {time(m.sent_at)}
          {m.edited_at ? " · edited" : ""}
          {m.direction === "out" ? " · " + m.status : ""}
        </small>
        <div className="bubble-tools">
          {actionable && c?.capabilities.edit && m.kind === "text" && (
            <button onClick={() => setEditing(true)} aria-label="Edit message">
              <Pencil />
            </button>
          )}
          {actionable && c?.capabilities.delete && (
            <button
              disabled={busy}
              onClick={() => {
                if (
                  window.confirm(
                    "Delete this message for everyone where supported?",
                  )
                )
                  void action(true);
              }}
              aria-label="Delete message"
            >
              <Trash2 />
            </button>
          )}
          {m.direction === "out" &&
            (m.status === "read" ? (
              <CheckCheck size={13} />
            ) : m.status === "sent" ? (
              <Check size={13} />
            ) : null)}
        </div>
      </div>
    </article>
  );
}

type Template = {
  name: string;
  language: string;
  status: string;
  components?: { type: string; text?: string }[];
};
function Composer({
  conversation: c,
  connection,
  stop,
  notice,
  changed,
}: {
  conversation: Conversation;
  connection?: Connection;
  stop: boolean;
  notice: (s: string) => void;
  changed: () => void;
}) {
  const [text, setText] = useState(""),
    [file, setFile] = useState<Media | null>(null),
    [busy, setBusy] = useState(false),
    [templates, setTemplates] = useState<Template[]>([]),
    [template, setTemplate] = useState<Template | null>(null),
    [params, setParams] = useState<string[]>([]),
    [picker, setPicker] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null),
    draftID = useRef(crypto.randomUUID());
  function newDraft() {
    draftID.current = crypto.randomUUID();
  }
  async function upload(f: File) {
    if (f.size > 25 * 1024 * 1024) {
      notice("Attachments must be under 25 MiB");
      return;
    }
    setBusy(true);
    try {
      const form = new FormData();
      form.append("file", f);
      const m = await api<Media>("/media", "POST", form);
      setFile(m);
      newDraft();
    } catch (e) {
      notice((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function send(e: FormEvent) {
    e.preventDefault();
    if (!connection) return;
    setBusy(true);
    try {
      let kind = "text";
      if (file)
        kind = file.mime.startsWith("image/")
          ? "image"
          : file.mime.startsWith("audio/")
            ? "audio"
            : file.mime.startsWith("video/")
              ? "video"
              : "document";
      const body: Record<string, unknown> = {
        text,
        kind,
        media_id: file?.id || "",
      };
      if (template)
        body.template = {
          name: template.name,
          language: { code: template.language },
          components: params.length
            ? [
                {
                  type: "body",
                  parameters: params.map((text) => ({ type: "text", text })),
                },
              ]
            : [],
        };
      await api("/conversations/" + c.id + "/messages", "POST", body, {
        "Idempotency-Key": draftID.current,
      });
      setText("");
      setFile(null);
      setTemplate(null);
      setParams([]);
      newDraft();
      changed();
    } catch (e) {
      notice((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function templatePicker() {
    if (!connection) return;
    try {
      const data = await api<{ data: Template[] }>(
        "/connections/" + connection.id + "/templates",
      );
      setTemplates(
        data.data.filter(
          (t) =>
            t.status === "APPROVED" &&
            !(t.components || []).some(
              (c) => c.type === "HEADER" || c.type === "BUTTONS",
            ),
        ),
      );
      setPicker(true);
    } catch (e) {
      notice((e as Error).message);
    }
  }
  return (
    <form className="composer" onSubmit={send}>
      {stop && (
        <div className="notice">The operator has paused all sending.</div>
      )}
      {template ? (
        <div>
          <div className="row spread">
            <small>Template: {template.name}</small>
            <button
              type="button"
              className="icon"
              onClick={() => {
                setTemplate(null);
                setParams([]);
                newDraft();
              }}
              aria-label="Remove template"
            >
              <X />
            </button>
          </div>
          {params.map((v, i) => (
            <label className="field" key={i}>
              Variable {i + 1}
              <input
                value={v}
                onChange={(e) => {
                  setParams((p) =>
                    p.map((s, j) => (i === j ? e.target.value : s)),
                  );
                  newDraft();
                }}
                required
              />
            </label>
          ))}
        </div>
      ) : (
        <textarea
          aria-label="Reply text"
          placeholder="Write a reply…"
          value={text}
          disabled={busy || stop}
          onChange={(e) => {
            setText(e.target.value);
            newDraft();
          }}
          maxLength={4096}
        />
      )}
      <div className="composer-actions">
        <div className="tools">
          <input
            className="visually-hidden"
            ref={fileInput}
            type="file"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void upload(f);
              e.target.value = "";
            }}
          />
          <button
            type="button"
            className="icon"
            aria-label="Attach file"
            disabled={
              busy || stop || !connection?.capabilities.media || !!template
            }
            onClick={() => fileInput.current?.click()}
          >
            <Paperclip />
          </button>
          {connection?.capabilities.templates && (
            <button
              type="button"
              disabled={busy}
              onClick={() => void templatePicker()}
            >
              Template
            </button>
          )}
          {file && (
            <button
              type="button"
              className="file-chip"
              onClick={() => {
                setFile(null);
                newDraft();
              }}
              aria-label="Remove attachment"
            >
              {file.name}
              <X size={12} />
            </button>
          )}
        </div>
        <button
          className="primary"
          disabled={
            busy ||
            stop ||
            connection?.status !== "connected" ||
            (!text.trim() && !file && !template)
          }
        >
          {busy ? "Sending…" : "Send"}
          <Send />
        </button>
      </div>
      {picker && (
        <Dialog title="Approved templates" onClose={() => setPicker(false)}>
          <p className="muted">
            Choose an approved text template to reply outside the service
            window.
          </p>
          {templates.length === 0 ? (
            <p>
              No approved text-only templates found. Create one in WhatsApp
              Manager.
            </p>
          ) : (
            templates.map((t) => (
              <button
                type="button"
                key={t.name + t.language}
                onClick={() => {
                  setTemplate(t);
                  setPicker(false);
                  const body =
                    t.components?.find((c) => c.type === "BODY")?.text || "";
                  const nums = [...body.matchAll(/\{\{(\d+)\}\}/g)].map((m) =>
                    Number(m[1]),
                  );
                  setParams(Array(Math.max(0, ...nums)).fill(""));
                  setFile(null);
                  setText("");
                  newDraft();
                }}
              >
                {t.name} · {t.language}
              </button>
            ))
          )}
        </Dialog>
      )}
    </form>
  );
}

function ConnectDialog({
  initial,
  me,
  close,
  notice,
  changed,
}: {
  initial: Connection | null;
  me: Me;
  close: () => void;
  notice: (s: string) => void;
  changed: () => void;
}) {
  const [provider, setProvider] = useState<Provider>(
      initial?.provider || "telegram",
    ),
    [label, setLabel] = useState(initial?.label || ""),
    [risk, setRisk] = useState(false),
    [connection, setConnection] = useState(initial),
    [step, setStep] = useState(""),
    [qr, setQR] = useState(""),
    [value, setValue] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [apiID, setAPIID] = useState(""),
    [apiHash, setAPIHash] = useState(""),
    [pin, setPin] = useState("");
  const existing = initial?.id;
  useEffect(() => {
    if (initial) setConnection(initial);
  }, [initial]);
  const id = connection?.id;
  useEffect(() => {
    if (!id || provider !== "whatsapp_web") return;
    let alive = true;
    const get = () =>
      api<{ step: string; qr?: string }>(
        "/connections/" + id + "/challenge",
        "POST",
        {},
      )
        .then((v) => {
          if (alive) {
            setStep(v.step);
            setQR(v.qr || "");
          }
        })
        .catch((e) => {
          if (alive) setError(e.message);
        });
    void get();
    const timer = setInterval(() => void get(), 3000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [id, provider]);
  async function execute(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await fn();
      changed();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function create() {
    let c = connection;
    if (!c) {
      c = await api<Connection>("/connections", "POST", {
        provider,
        label: label || labels[provider],
        risk_accepted: risk,
        credentials:
          provider === "telegram" && apiID
            ? { api_id: apiID, api_hash: apiHash }
            : undefined,
      });
      setConnection(c);
    }
    if (provider === "instagram") {
      const v = await api<{ url: string }>("/connections/" + c.id + "/oauth");
      location.assign(v.url);
    } else if (provider === "whatsapp_cloud") {
      const config = await api<{
        app_id: string;
        config_id: string;
        version: string;
        state: string;
      }>("/connections/" + c.id + "/oauth");
      const result = await embeddedSignup(config);
      await api("/connections/" + c.id + "/embedded-signup", "POST", {
        ...result,
        state: config.state,
        pin,
      });
      close();
      notice("WhatsApp authorization saved");
    } else if (provider === "telegram") {
      if (existing) {
        const v = await api<{ step: string }>(
          "/connections/" + c.id + "/challenge",
          "POST",
          { action: "reconnect" },
        );
        if (v.step === "connected") {
          close();
          notice("Account connected");
        } else setStep(v.step);
      } else setStep("phone");
    } else {
      if (existing)
        await api("/connections/" + c.id + "/challenge", "POST", {
          action: "reconnect",
        });
      setStep("qr");
    }
  }
  async function challenge(e: FormEvent) {
    e.preventDefault();
    await execute(async () => {
      const v = await api<{ step: string }>(
        "/connections/" + connection!.id + "/challenge",
        "POST",
        { [step]: value },
      );
      setStep(v.step);
      setValue("");
      if (v.step === "connected") {
        notice("Account connected");
        close();
      }
    });
  }
  return (
    <Dialog
      title={existing ? "Channel settings" : "Connect a channel"}
      onClose={close}
    >
      {!connection && (
        <>
          <div className="provider-select">
            {(
              [
                "telegram",
                "whatsapp_cloud",
                "whatsapp_web",
                "instagram",
              ] as Provider[]
            ).map((p) => (
              <button
                key={p}
                className={provider === p ? "selected" : ""}
                disabled={busy}
                onClick={() => {
                  setProvider(p);
                  setError("");
                }}
              >
                {p === "telegram" ? (
                  <Send />
                ) : p === "instagram" ? (
                  <Camera />
                ) : (
                  <MessageCircle />
                )}
                {p === "whatsapp_cloud"
                  ? "WA official"
                  : p === "whatsapp_web"
                    ? "WA unofficial"
                    : labels[p]}
              </button>
            ))}
          </div>
          <label className="field">
            Account label
            <input
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. Support team"
              maxLength={100}
            />
          </label>
        </>
      )}
      {provider === "whatsapp_cloud" &&
        (!connection || connection.status !== "connected") && (
          <label className="field">
            WhatsApp two-step PIN
            <input
              type="password"
              inputMode="numeric"
              value={pin}
              onChange={(e) =>
                setPin(e.target.value.replace(/[^0-9]/g, "").slice(0, 6))
              }
              maxLength={6}
            />
            <small>
              Enter the existing six-digit PIN, or choose one for a new number.
              Keep it in your password manager.
            </small>
          </label>
        )}
      {provider === "whatsapp_web" && (
        <div className="notice">
          Unofficial access can cause restrictions or a permanent account ban,
          even with careful use. Sessions grant access to private chats. This
          connector is reply-only and has no delivery guarantee.
        </div>
      )}
      {provider === "whatsapp_web" && !connection && (
        <label className="check">
          <input
            type="checkbox"
            checked={risk}
            onChange={(e) => setRisk(e.target.checked)}
          />
          I understand and accept the account, privacy, and reliability risks.
        </label>
      )}
      {connection && (
        <>
          <div className="row spread">
            <span className="row">
              <ChannelIcon provider={provider} />
              <strong>{connection.label}</strong>
            </span>
            <span className={"status " + connection.status}>
              {connection.status.replace("_", " ")}
            </span>
          </div>
          <p className="muted">{connection.identity || labels[provider]}</p>
          {connection.detail && (
            <div className="notice">{connection.detail}</div>
          )}
        </>
      )}
      {provider === "telegram" && !me.config.telegram && !connection && (
        <>
          <p className="muted">
            Enter your Telegram application credentials from
            my.telegram.org/apps.
          </p>
          <label className="field">
            API ID
            <input
              value={apiID}
              onChange={(e) => setAPIID(e.target.value)}
              inputMode="numeric"
            />
          </label>
          <label className="field">
            API hash
            <input
              type="password"
              value={apiHash}
              onChange={(e) => setAPIHash(e.target.value)}
            />
          </label>
        </>
      )}
      {qr && (
        <img
          className="qr"
          src={qr}
          alt="WhatsApp linked-device pairing QR"
          width={256}
          height={256}
        />
      )}{" "}
      {provider === "whatsapp_web" && connection && step === "waiting" && (
        <p className="muted">Waiting for a pairing QR or connection…</p>
      )}
      {["phone", "code", "password"].includes(step) && (
        <form className="form" onSubmit={challenge}>
          <label className="field">
            {step === "phone"
              ? "Phone number with country code"
              : step === "code"
                ? "Telegram login code"
                : "Two-factor password"}
            <input
              type={step === "password" ? "password" : "text"}
              autoComplete="off"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder={step === "phone" ? "+7…" : undefined}
              required
            />
          </label>
          <button className="primary" disabled={busy || !value}>
            Continue
          </button>
        </form>
      )}
      {error && (
        <div className="notice error-notice" role="alert">
          {error}
        </div>
      )}
      {!step && (!connection || connection.status !== "connected") && (
        <button
          className="primary"
          disabled={
            busy ||
            (provider === "whatsapp_cloud" && pin.length !== 6) ||
            (provider === "whatsapp_web" &&
              (!me.config.whatsapp_web || (!risk && !connection))) ||
            (["instagram", "whatsapp_cloud"].includes(provider) &&
              !me.config[provider])
          }
          onClick={() => void execute(create)}
        >
          {busy
            ? "Connecting…"
            : provider === "instagram" || provider === "whatsapp_cloud"
              ? "Connect with Meta"
              : provider === "whatsapp_web"
                ? "Pair with QR"
                : "Connect Telegram"}
          <ArrowUpRight />
        </button>
      )}
      {!me.config[provider] && provider !== "telegram" && (
        <p className="muted">
          The operator must enable this integration in the service environment.
        </p>
      )}
      {connection && (
        <div className="divider form">
          <div className="row">
            {connection.status === "connected" ? (
              <button
                disabled={busy}
                onClick={() =>
                  void execute(async () => {
                    await api("/connections/" + id + "/challenge", "POST", {
                      action: "pause",
                    });
                    notice("Channel paused");
                    close();
                  })
                }
              >
                Pause
              </button>
            ) : (
              <button
                disabled={busy}
                onClick={() =>
                  void execute(async () => {
                    const v = await api<{ step: string; qr?: string }>(
                      "/connections/" + id + "/challenge",
                      "POST",
                      { action: "reconnect" },
                    );
                    setStep(v.step);
                    setQR(v.qr || "");
                    if (v.step === "connected") {
                      close();
                      notice("Account connected");
                    }
                  })
                }
              >
                Reconnect
              </button>
            )}
            {connection.capabilities.history && (
              <button
                disabled={busy || connection.status !== "connected"}
                onClick={() =>
                  void execute(async () => {
                    await api("/connections/" + id + "/import", "POST", {});
                    notice("Bounded history import queued");
                  })
                }
              >
                Import history
              </button>
            )}
          </div>
          {connection.status === "disconnected" ? (
            <button
              className="danger"
              disabled={busy}
              onClick={() =>
                void execute(async () => {
                  if (
                    !window.confirm(
                      "Permanently delete this account’s stored messages and attachments?",
                    )
                  )
                    return;
                  await api("/connections/" + id, "DELETE");
                  close();
                  notice("Channel data deleted");
                })
              }
            >
              <Trash2 />
              Delete stored data
            </button>
          ) : (
            <button
              className="danger"
              disabled={busy}
              onClick={() =>
                void execute(async () => {
                  if (
                    !window.confirm(
                      "Disconnect this account and revoke its session?",
                    )
                  )
                    return;
                  await api("/connections/" + id + "/disconnect", "POST", {});
                  close();
                  notice("Channel disconnected");
                })
              }
            >
              Disconnect account
            </button>
          )}
        </div>
      )}
    </Dialog>
  );
}

type Key = { id: string; name: string; prefix: string };
type Member = { id: string; name: string; email: string; role: string };
type Webhook = { id: string; url: string; enabled: boolean };
type Job = {
  id: string;
  kind: string;
  state: string;
  attempts: number;
  error: string;
};
type Delivery = { id: string; state: string; attempts: number; error: string };
function WorkspaceDialog({
  close,
  notice,
}: {
  close: () => void;
  notice: (s: string) => void;
}) {
  const [tab, setTab] = useState("keys"),
    [keys, setKeys] = useState<Key[]>([]),
    [members, setMembers] = useState<Member[]>([]),
    [webhooks, setWebhooks] = useState<Webhook[]>([]),
    [jobs, setJobs] = useState<Job[]>([]),
    [deliveries, setDeliveries] = useState<Delivery[]>([]),
    [secret, setSecret] = useState(""),
    [input, setInput] = useState(""),
    [busy, setBusy] = useState(false),
    [role, setRole] = useState("member"),
    [error, setError] = useState("");
  async function load() {
    try {
      const [k, m, h, j, d] = await Promise.all([
        api<Key[]>("/api-keys"),
        api<Member[]>("/members"),
        api<Webhook[]>("/webhooks"),
        api<Job[]>("/jobs"),
        api<Delivery[]>("/webhook-deliveries"),
      ]);
      setKeys(k);
      setMembers(m);
      setWebhooks(h);
      setJobs(j);
      setDeliveries(d);
    } catch (e) {
      setError((e as Error).message);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  async function execute(f: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await f();
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog title="Workspace" onClose={close}>
      <div className="tabs">
        {["keys", "team", "webhooks", "activity"].map((t) => (
          <button
            key={t}
            className={tab === t ? "selected" : ""}
            onClick={() => {
              setTab(t);
              setInput("");
              setSecret("");
            }}
          >
            {t === "keys" ? (
              <KeyRound />
            ) : t === "team" ? (
              <Users />
            ) : (
              <Settings />
            )}
            {t === "keys" ? "API keys" : t.charAt(0).toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>
      {error && (
        <div className="notice error-notice" role="alert">
          {error}
        </div>
      )}
      {secret && (
        <>
          <div className="notice">
            Save this now. Secrets are shown only once.
          </div>
          <div className="secret">{secret}</div>
          <button
            onClick={() =>
              void navigator.clipboard
                .writeText(secret)
                .then(() => notice("Copied"))
                .catch(() => notice("Select and copy the value manually"))
            }
          >
            <Copy />
            Copy
          </button>
        </>
      )}
      {tab === "keys" && (
        <>
          <p className="muted">
            Keys let a CRM read conversations and send messages. They cannot
            administer the workspace.
          </p>
          {keys.map((k) => (
            <div className="settings-row" key={k.id}>
              <div className="info">
                <strong>{k.name}</strong>
                <small>{k.prefix}••••••••</small>
              </div>
              <button
                className="danger icon"
                aria-label={"Revoke " + k.name}
                disabled={busy}
                onClick={() =>
                  void execute(async () => {
                    await api("/api-keys/" + k.id, "DELETE");
                    notice("API key revoked");
                  })
                }
              >
                <Trash2 />
              </button>
            </div>
          ))}
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void execute(async () => {
                const v = await api<{ token: string }>("/api-keys", "POST", {
                  name: input,
                });
                setSecret(v.token);
                setInput("");
              });
            }}
          >
            <label className="field">
              Key name
              <input
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder="e.g. Stepline CRM"
                required
              />
            </label>
            <button className="primary" disabled={busy}>
              Create key
            </button>
          </form>
        </>
      )}
      {tab === "team" && (
        <>
          {members.map((m) => (
            <div className="settings-row" key={m.id}>
              <div className="info">
                <strong>{m.name || m.email}</strong>
                <small>
                  {m.email} · {m.role}
                </small>
              </div>
              <button
                className="danger icon"
                disabled={busy}
                aria-label={"Remove " + m.email}
                onClick={() =>
                  void execute(async () => {
                    if (
                      !window.confirm(
                        "Remove this teammate and revoke their sessions?",
                      )
                    )
                      return;
                    await api("/members/" + m.id, "DELETE");
                  })
                }
              >
                <Trash2 />
              </button>
            </div>
          ))}
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void execute(async () => {
                const v = await api<{ url: string }>("/invitations", "POST", {
                  email: input,
                  role,
                });
                setSecret(v.url);
                setInput("");
              });
            }}
          >
            <label className="field">
              Invite by email
              <input
                type="email"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                required
              />
            </label>
            <label className="field">
              Role
              <select value={role} onChange={(e) => setRole(e.target.value)}>
                <option value="member">Member · read and reply</option>
                <option value="admin">Admin · manage workspace</option>
              </select>
            </label>
            <button className="primary" disabled={busy}>
              Create invite link
            </button>
            <small>Share the link yourself. It expires in 48 hours.</small>
          </form>
        </>
      )}
      {tab === "webhooks" && (
        <>
          <p className="muted">
            Send signed message and connection events to your CRM’s public HTTPS
            endpoint.
          </p>
          {webhooks.map((h) => (
            <div className="settings-row" key={h.id}>
              <span className="info">{h.url}</span>
              <button
                className="danger icon"
                aria-label="Remove webhook"
                disabled={busy}
                onClick={() =>
                  void execute(async () => {
                    await api("/webhooks/" + h.id, "DELETE");
                  })
                }
              >
                <Trash2 />
              </button>
            </div>
          ))}
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void execute(async () => {
                const v = await api<{ secret: string }>("/webhooks", "POST", {
                  url: input,
                });
                setSecret(v.secret);
                setInput("");
              });
            }}
          >
            <label className="field">
              Webhook URL
              <input
                type="url"
                placeholder="https://crm.example.com/events"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                required
              />
            </label>
            <button className="primary" disabled={busy}>
              Add webhook
            </button>
          </form>
          {deliveries.slice(0, 10).map((d) => (
            <div className="settings-row" key={d.id}>
              <span className="info">
                {d.state} · {d.attempts} attempts<small>{d.error}</small>
              </span>
            </div>
          ))}
        </>
      )}
      {tab === "activity" && (
        <>
          <div className="row spread">
            <p className="muted">Recent sends, imports, and webhook jobs.</p>
            <button
              className="icon"
              aria-label="Refresh activity"
              onClick={() => void load()}
            >
              <RefreshCw />
            </button>
          </div>
          {jobs.length === 0 ? (
            <p className="muted">No activity yet.</p>
          ) : (
            jobs.map((j) => (
              <div className="settings-row" key={j.id}>
                <span className="info">
                  <strong>
                    {j.kind} · {j.state}
                  </strong>
                  <small>{j.error || j.attempts + " attempts"}</small>
                </span>
              </div>
            ))
          )}
        </>
      )}
    </Dialog>
  );
}
