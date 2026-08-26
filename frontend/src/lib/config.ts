import { createContext, useContext } from "react";
import type { AppConfig } from "./api";

export const ConfigContext = createContext<AppConfig>({
  app_name: "Flimm",
  oidc_issuer: "",
  oidc_client_id: "",
  version: "dev",
});

export function useConfig() {
  return useContext(ConfigContext);
}
