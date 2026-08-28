/// <reference types="vite/client" />

// Baked in at image build time (see Dockerfile). All optional: a build without
// them ships without Sentry and without analytics.
interface ImportMetaEnv {
  readonly VITE_APP_VERSION?: string;
  readonly VITE_SENTRY_DSN?: string;
  /** Self-hosted Umami origin, e.g. https://stats.example.com. */
  readonly VITE_UMAMI_URL?: string;
  /** The website's id in that Umami instance. */
  readonly VITE_UMAMI_WEBSITE_ID?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
