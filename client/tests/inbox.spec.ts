import { test, expect, type Page } from "@playwright/test";
const conn = {
  id: "a",
  provider: "telegram",
  label: "Support Telegram",
  identity: "@support_test",
  status: "connected",
  detail: "Fixture account",
  capabilities: {
    groups: true,
    media: true,
    voice: true,
    edit: true,
    delete: true,
    templates: false,
    history: true,
  },
};
async function fixtures(page: Page, role = "admin", authenticated = true) {
  let logged = authenticated;
  let key = false;
  let messages = [
    {
      id: "m1",
      direction: "in",
      kind: "text",
      text: "Can you help with my order?",
      sender: "Test customer",
      status: "received",
      sent_at: "2026-10-13T10:00:00Z",
    },
  ];
  await page.route("**/api/v1/**", async (route) => {
    const r = route.request(),
      url = new URL(r.url()),
      path = url.pathname.replace("/api/v1", "");
    if (path === "/events") {
      await route.abort();
      return;
    }
    let data: unknown = { ok: true };
    let status = 200;
    if (path === "/me") {
      if (!logged) {
        status = 401;
        data = { error: "login required" };
      } else
        data = {
          id: "u",
          name: "Test operator",
          email: "operator@example.com",
          role,
          config: {
            telegram: true,
            whatsapp_web: true,
            instagram: false,
            whatsapp_cloud: false,
            stop_all_sends: false,
          },
        };
    } else if (path === "/auth/login") {
      logged = true;
    } else if (path === "/connections" && r.method() === "GET") data = [conn];
    else if (path === "/conversations")
      data = [
        {
          id: "c",
          connection_id: "a",
          title: "Test customer",
          provider: "telegram",
          account_label: conn.label,
          preview: "Can you help with my order?",
          unread: 1,
          is_group: false,
          last_message_at: "2026-10-13T10:00:00Z",
        },
      ];
    else if (path === "/conversations/c/messages") {
      if (r.method() === "POST") {
        const body = r.postDataJSON();
        messages = [
          ...messages,
          {
            id: "m2",
            direction: "out",
            kind: "text",
            text: body.text,
            sender: "Test operator",
            status: "queued",
            sent_at: "2026-10-13T10:01:00Z",
          },
        ];
        status = 202;
        data = { id: "m2", status: "queued" };
      } else data = [...messages].reverse();
    } else if (path === "/api-keys") {
      if (r.method() === "POST") {
        key = true;
        data = { id: "k", token: "ms_fixture_one_time_secret" };
        status = 201;
      } else
        data = key ? [{ id: "k", name: "Test CRM", prefix: "ms_fixture" }] : [];
    } else if (path === "/members")
      data = [
        { id: "u", name: "Test operator", email: "operator@example.com", role },
      ];
    else if (["/webhooks", "/jobs", "/webhook-deliveries"].includes(path))
      data = [];
    else if (path === "/connections" && r.method() === "POST") {
      data = {
        ...conn,
        id: "b",
        provider: r.postDataJSON().provider,
        label: r.postDataJSON().label,
        status: "needs_auth",
      };
      status = 201;
    } else if (path.includes("/challenge")) data = { step: "phone" };
    await route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify(data),
    });
  });
}
test("team can select a conversation and queue a reply", async ({ page }) => {
  await fixtures(page);
  await page.goto("/");
  await page.getByRole("button", { name: /Test customer/ }).click();
  await expect(
    page.getByText("Can you help with my order?", { exact: true }).last(),
  ).toBeVisible();
  await page.getByLabel("Reply text").fill("Yes, I can help.");
  const request = page.waitForRequest(
    (r) =>
      r.method() === "POST" && r.url().endsWith("/conversations/c/messages"),
  );
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const sent = await request;
  expect(sent.headers()["idempotency-key"]).toMatch(/^[0-9a-f-]{36}$/);
  await expect(
    page.getByText("Yes, I can help.", { exact: true }),
  ).toBeVisible();
});
test("API secret is shown once and omitted after reopening", async ({
  page,
}) => {
  await fixtures(page);
  await page.goto("/");
  await page.getByRole("button", { name: "API & workspace" }).click();
  await page.getByLabel("Key name").fill("Test CRM");
  await page.getByRole("button", { name: "Create key" }).click();
  await expect(
    page.getByText("ms_fixture_one_time_secret", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close dialog" }).click();
  await page.getByRole("button", { name: "API & workspace" }).click();
  await expect(
    page.getByText("ms_fixture_one_time_secret", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText("ms_fixture••••••••", { exact: true }),
  ).toBeVisible();
});
test("member has inbox access without workspace administration", async ({
  page,
}) => {
  await fixtures(page, "member");
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Messages", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "API & workspace" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Connect", exact: true }),
  ).toHaveCount(0);
});
test("login form handles team authentication", async ({ page }) => {
  await fixtures(page, "admin", false);
  await page.goto("/");
  await page.getByLabel("Email", { exact: true }).fill("operator@example.com");
  await page.getByLabel("Password", { exact: true }).fill("long-test-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByText("Test operator", { exact: true })).toBeVisible();
});
for (const width of [320, 375, 414, 768, 1440])
  test(`wireframe layout fits ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await fixtures(page);
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Channels" })).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBeTruthy();
    const controls = await Promise.all(
      ["Refresh inbox", "API & workspace", "Sign out"].map((name) =>
        page.getByRole("button", { name, exact: true }).boundingBox(),
      ),
    );
    for (let i = 0; i < controls.length; i++)
      for (let j = i + 1; j < controls.length; j++) {
        const a = controls[i]!,
          b = controls[j]!;
        expect(
          a.x + a.width <= b.x ||
            b.x + b.width <= a.x ||
            a.y + a.height <= b.y ||
            b.y + b.height <= a.y,
        ).toBeTruthy();
      }
    await page.getByRole("button", { name: /Test customer/ }).click();
    await expect(page.getByLabel("Reply text")).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBeTruthy();
    await page.screenshot({
      path: `test-results/chat-${width}.png`,
      fullPage: true,
    });
    if (width < 768) {
      await page.getByRole("button", { name: "Back to messages" }).click();
      await expect(
        page.getByRole("heading", { name: "Channels" }),
      ).toBeVisible();
    }
    await page.getByRole("button", { name: "Connect", exact: true }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBeTruthy();
    if (width === 1440) {
      await page.getByRole("button", { name: "Close dialog" }).click();
      await page.screenshot({
        path: "test-results/inbox-desktop.png",
        fullPage: true,
      });
    }
  });
test("unofficial connection requires risk acknowledgement", async ({
  page,
}) => {
  await fixtures(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await page
    .getByRole("button", { name: "WA unofficial", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Pair with QR" }),
  ).toBeDisabled();
  await page.getByRole("checkbox").check();
  await expect(
    page.getByRole("button", { name: "Pair with QR" }),
  ).toBeEnabled();
  await expect(page.getByText(/permanent account ban/)).toBeVisible();
});
