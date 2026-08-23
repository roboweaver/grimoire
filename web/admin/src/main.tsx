import React from "react";
import ReactDOM from "react-dom/client";
import { defaultTheme, Provider } from "@adobe/react-spectrum";
import {
  BrowserRouter,
  useHref,
  useNavigate,
  type NavigateOptions,
} from "react-router-dom";
import { App } from "./App";

declare module "@adobe/react-spectrum" {
  interface RouterConfig {
    routerOptions: NavigateOptions;
  }
}

// Bridge Spectrum's client-side navigation to react-router so Spectrum links
// and pressables use SPA routing instead of full page loads.
function SpectrumRouterProvider({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  return (
    <Provider
      theme={defaultTheme}
      router={{ navigate, useHref }}
      minHeight="100vh"
    >
      {children}
    </Provider>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <BrowserRouter basename="/admin">
      <SpectrumRouterProvider>
        <App />
      </SpectrumRouterProvider>
    </BrowserRouter>
  </React.StrictMode>,
);
