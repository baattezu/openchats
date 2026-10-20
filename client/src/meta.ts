type SignupConfig = {
  app_id: string;
  config_id: string;
  version: string;
  state: string;
};
type FBResponse = { authResponse?: { code?: string } };
declare global {
  interface Window {
    FB?: {
      init: (x: Record<string, unknown>) => void;
      login: (f: (r: FBResponse) => void, o: Record<string, unknown>) => void;
    };
  }
}
export async function embeddedSignup(
  config: SignupConfig,
): Promise<{ code: string; waba_id: string; phone_number_id: string }> {
  if (!window.FB)
    await new Promise<void>((resolve, reject) => {
      const script = document.createElement("script");
      script.src = "https://connect.facebook.net/en_US/sdk.js";
      script.async = true;
      script.onload = () => resolve();
      script.onerror = () => reject(new Error("Meta SDK could not load"));
      document.head.append(script);
    });
  window.FB!.init({
    appId: config.app_id,
    version: config.version,
    cookie: true,
    xfbml: false,
  });
  return new Promise((resolve, reject) => {
    let code = "",
      waba = "",
      phone = "";
    const timeout = window.setTimeout(
      () => finish(new Error("Signup timed out; try again")),
      180_000,
    );
    function finish(error?: Error) {
      window.clearTimeout(timeout);
      window.removeEventListener("message", listener);
      if (error) reject(error);
      else resolve({ code, waba_id: waba, phone_number_id: phone });
    }
    function check() {
      if (code && waba && phone) finish();
    }
    function listener(event: MessageEvent) {
      if (
        ![
          "https://www.facebook.com",
          "https://web.facebook.com",
          "https://business.facebook.com",
        ].includes(event.origin)
      )
        return;
      let data;
      try {
        data =
          typeof event.data === "string" ? JSON.parse(event.data) : event.data;
      } catch {
        return;
      }
      if (data?.type !== "WA_EMBEDDED_SIGNUP") return;
      if (
        [
          "FINISH",
          "FINISH_ONLY_WABA",
          "FINISH_WHATSAPP_BUSINESS_APP_ONBOARDING",
        ].includes(data.event)
      ) {
        waba = data.data?.waba_id || "";
        phone = data.data?.phone_number_id || "";
        check();
      } else if (["CANCEL", "ERROR"].includes(data.event))
        finish(new Error("Meta signup was cancelled or failed"));
    }
    window.addEventListener("message", listener);
    window.FB!.login(
      (response) => {
        code = response.authResponse?.code || "";
        if (!code) finish(new Error("Meta authorization was cancelled"));
        else check();
      },
      {
        config_id: config.config_id,
        response_type: "code",
        override_default_response_type: true,
        extras: {},
      },
    );
  });
}
