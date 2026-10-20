export type Provider =
  "whatsapp_web" | "whatsapp_cloud" | "instagram" | "telegram";
export type Caps = {
  groups: boolean;
  media: boolean;
  voice: boolean;
  edit: boolean;
  delete: boolean;
  templates: boolean;
  history: boolean;
};
export type Connection = {
  id: string;
  provider: Provider;
  label: string;
  identity: string;
  status: string;
  detail: string;
  capabilities: Caps;
};
export type Conversation = {
  id: string;
  connection_id: string;
  title: string;
  provider: Provider;
  account_label: string;
  preview: string;
  unread: number;
  is_group: boolean;
  last_message_at: string;
};
export type Media = { id: string; name: string; mime: string; size: number };
export type Message = {
  id: string;
  direction: "in" | "out";
  kind: string;
  text: string;
  sender: string;
  status: string;
  sent_at: string;
  external_id?: string;
  media?: Media;
  edited_at?: string;
  deleted_at?: string;
};
export type Me = {
  id: string;
  name: string;
  email: string;
  role: string;
  config: Record<string, boolean>;
};
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  method = "GET",
  body?: unknown,
  headers?: Record<string, string>,
): Promise<T> {
  const r = await fetch("/api/v1" + path, {
    method,
    credentials: "same-origin",
    headers: {
      ...(body instanceof FormData
        ? {}
        : { "Content-Type": "application/json" }),
      ...headers,
    },
    body:
      body === undefined
        ? undefined
        : body instanceof FormData
          ? body
          : JSON.stringify(body),
  });
  const data = await r.json();
  if (!r.ok) throw new ApiError(r.status, data.error || "Request failed");
  return data as T;
}
export const labels: Record<Provider, string> = {
  whatsapp_web: "WhatsApp · unofficial",
  whatsapp_cloud: "WhatsApp · official",
  instagram: "Instagram",
  telegram: "Telegram",
};
