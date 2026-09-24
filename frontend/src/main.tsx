import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import * as Sentry from "@sentry/react";
import "./index.css";
import App from "./App";

// DSN and version are baked in at build time (VITE_*); local dev has neither,
// so Sentry stays off there.
if (import.meta.env.VITE_SENTRY_DSN) {
  Sentry.init({
    dsn: import.meta.env.VITE_SENTRY_DSN,
    release: `flimm@${import.meta.env.VITE_APP_VERSION || "dev"}`,
    environment: "production",
    integrations: [Sentry.browserTracingIntegration()],
    tracesSampleRate: 0.2,
    // SDK v11 collects request/response bodies, headers, cookies, query
    // params, user info and stack-frame locals by default. Opt out of all of
    // it: errors and traces are enough, the payloads are personal data.
    dataCollection: {
      userInfo: false,
      cookies: false,
      httpHeaders: false,
      httpBodies: [],
      urlQueryParams: false,
      stackFrameVariables: false,
    },
  });
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Sentry.ErrorBoundary
      fallback={
        <div className="flex min-h-screen flex-col items-center justify-center gap-4 p-6 text-center">
          <p className="h1">Something went wrong.</p>
          <button className="btn pri" onClick={() => window.location.reload()}>
            Reload
          </button>
        </div>
      }
    >
      <App />
    </Sentry.ErrorBoundary>
  </StrictMode>,
);
