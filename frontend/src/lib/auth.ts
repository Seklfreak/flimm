import type { UserManagerSettings } from "oidc-client-ts";

// OIDC settings come from the backend at runtime (GET /api/v1/config), so the
// issuer/client id live in the deployment, not in the bundle.
export function makeOidcConfig(
  issuer: string,
  clientId: string,
): UserManagerSettings & { onSigninCallback?: () => void } {
  return {
    authority: issuer,
    client_id: clientId,
    redirect_uri: window.location.origin + "/",
    post_logout_redirect_uri: window.location.origin + "/",
    scope: "openid profile email offline_access",
    automaticSilentRenew: true,
    // Strip ?code&state after login and go back to where the user started.
    onSigninCallback: () => {
      let target = "/";
      try {
        target = sessionStorage.getItem(RETURN_KEY) || "/";
        sessionStorage.removeItem(RETURN_KEY);
      } catch {
        /* storage unavailable */
      }
      window.history.replaceState({}, document.title, target);
    },
  };
}

export const RETURN_KEY = "archive:returnTo";

// Remember the deep link before bouncing to the issuer.
export function rememberReturnTo() {
  try {
    sessionStorage.setItem(
      RETURN_KEY,
      window.location.pathname + window.location.search + window.location.hash,
    );
  } catch {
    /* ignore */
  }
}

// Current access token, kept in sync from React (AuthGate) so the non-React
// API client can attach it synchronously.
let currentAccessToken: string | null = null;
export function setAccessToken(token: string | null) {
  currentAccessToken = token;
}
export function getAccessToken(): string | null {
  return currentAccessToken;
}

// Bridge so the API client can trigger a re-login on 401.
let unauthorizedHandler: (() => void) | null = null;
export function setUnauthorizedHandler(fn: (() => void) | null) {
  unauthorizedHandler = fn;
}
export function handleUnauthorized() {
  unauthorizedHandler?.();
}
