import { useEffect, useMemo, useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider, useAuth } from "react-oidc-context";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router";
import { api, type AppConfig } from "@/lib/api";
import { makeOidcConfig, rememberReturnTo, setAccessToken, setUnauthorizedHandler } from "@/lib/auth";
import { refreshMediaSession } from "@/lib/media";
import { routePattern, startAnalytics, trackScreen } from "@/lib/analytics";
import { useMe } from "@/lib/queries";
import { Spinner } from "@/components/ui";
import { Layout } from "@/components/Layout";
import { ConfigContext } from "@/lib/config";
import HomePage from "@/pages/HomePage";
import FeedPage from "@/pages/FeedPage";
import FeedEditorPage from "@/pages/FeedEditorPage";
import ChannelsPage from "@/pages/ChannelsPage";
import ChannelPage from "@/pages/ChannelPage";
import PlaylistsPage from "@/pages/PlaylistsPage";
import PlaylistPage from "@/pages/PlaylistPage";
import HistoryPage from "@/pages/HistoryPage";
import StatsPage from "@/pages/StatsPage";
import SearchPage from "@/pages/SearchPage";
import SettingsPage from "@/pages/SettingsPage";
import AdminPage from "@/pages/AdminPage";
import WatchPage from "@/pages/WatchPage";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 15_000 },
  },
});

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ConfigLoader>
        <BrowserRouter>
          <ThemeSync />
          <RouteAnalytics />
          <Routes>
            <Route element={<Layout />}>
              <Route index element={<HomePage />} />
              <Route path="/feeds/new" element={<FeedEditorPage />} />
              <Route path="/feeds/:id" element={<FeedPage />} />
              <Route path="/feeds/:id/edit" element={<FeedPage editing />} />
              <Route path="/channels" element={<ChannelsPage />} />
              <Route path="/channels/:id" element={<ChannelPage />} />
              <Route path="/playlists" element={<PlaylistsPage />} />
              <Route path="/playlists/:id" element={<PlaylistPage />} />
              <Route path="/history" element={<HistoryPage />} />
              <Route path="/stats" element={<StatsPage />} />
              <Route path="/search" element={<SearchPage />} />
              <Route path="/settings" element={<SettingsPage />} />
              <Route path="/admin" element={<AdminPage />} />
              <Route path="/watch/:id" element={<WatchPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </ConfigLoader>
    </QueryClientProvider>
  );
}

// Fetch /config (unauthenticated) first, then mount the OIDC provider with
// the discovered issuer. An empty issuer means the backend runs with
// AUTH_DISABLED, so we skip login entirely.
function ConfigLoader({ children }: { children: ReactNode }) {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .config()
      .then((c) => {
        if (!cancelled) setConfig(c);
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // The tracker is baked in at build time, but the deployment gets the last
  // word: a server running ANALYTICS_DISABLED=true is never reported to.
  useEffect(() => {
    if (config) startAnalytics(!config.analytics_disabled);
  }, [config]);

  const oidc = useMemo(
    () => (config?.oidc_issuer ? makeOidcConfig(config.oidc_issuer, config.oidc_client_id) : null),
    [config],
  );

  if (error) {
    return (
      <Center>
        <p className="meta">Could not reach the server: {error}</p>
        <button className="btn" onClick={() => window.location.reload()}>
          Retry
        </button>
      </Center>
    );
  }
  if (!config) return <Center><Spinner label="Loading…" /></Center>;

  setDocumentTitle(config.app_name);
  const tree = (
    <ConfigContext.Provider value={config}>
      <Bootstrap>{children}</Bootstrap>
    </ConfigContext.Provider>
  );
  return oidc ? (
    <AuthProvider {...oidc}>
      <AuthGate>{tree}</AuthGate>
    </AuthProvider>
  ) : (
    tree
  );
}

function setDocumentTitle(name: string) {
  // Not a hook call inside a conditional: ConfigLoader only reaches here once
  // config exists, but hooks must run unconditionally — so set it directly.
  if (typeof document !== "undefined" && name && document.title !== name) document.title = name;
}

function AuthGate({ children }: { children: ReactNode }) {
  const auth = useAuth();
  setAccessToken(auth.isAuthenticated ? (auth.user?.access_token ?? null) : null);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      rememberReturnTo();
      void auth.signinRedirect();
    });
    return () => setUnauthorizedHandler(null);
  }, [auth]);

  useEffect(() => {
    if (!auth.isLoading && !auth.isAuthenticated && !auth.error && !auth.activeNavigator) {
      rememberReturnTo();
      void auth.signinRedirect();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [auth.isLoading, auth.isAuthenticated, auth.error, auth.activeNavigator]);

  if (auth.error) {
    return (
      <Center>
        <p className="text-danger text-sm font-semibold">Sign-in failed: {auth.error.message}</p>
        <button className="btn pri" onClick={() => void auth.signinRedirect()}>
          Try again
        </button>
      </Center>
    );
  }
  if (!auth.isAuthenticated) return <Center><Spinner label="Signing in…" /></Center>;
  return <>{children}</>;
}

// Once we can call the API: load /me (prefs) and set the media cookie.
function Bootstrap({ children }: { children: ReactNode }) {
  const me = useMe();
  useEffect(() => {
    void refreshMediaSession();
  }, []);
  if (me.isLoading) return <Center><Spinner label="Loading…" /></Center>;
  if (me.isError) {
    return (
      <Center>
        <p className="meta">Could not load your profile: {me.error.message}</p>
        <button className="btn" onClick={() => me.refetch()}>
          Retry
        </button>
      </Center>
    );
  }
  return <>{children}</>;
}

// One pageview per navigation, reported as the route *pattern* — `/watch/:id`,
// never the video id. See lib/analytics.ts.
function RouteAnalytics() {
  const { pathname } = useLocation();
  useEffect(() => {
    const route = routePattern(pathname);
    if (route) trackScreen(route.url, route.title);
  }, [pathname]);
  return null;
}

// prefs.theme → <html data-theme>; "system" removes the attribute so the
// prefers-color-scheme media query decides.
function ThemeSync() {
  const me = useMe();
  const theme = me.data?.prefs.theme ?? "system";
  useEffect(() => {
    const el = document.documentElement;
    if (theme === "system") el.removeAttribute("data-theme");
    else el.setAttribute("data-theme", theme);
  }, [theme]);
  return null;
}

function Center({ children }: { children: ReactNode }) {
  return <div className="flex min-h-screen flex-col items-center justify-center gap-4 p-6 text-center">{children}</div>;
}
