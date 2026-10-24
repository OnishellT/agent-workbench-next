import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError, postSettings, type BootstrapDTO, type SettingsDTO } from "./api";
import { readRoute, type Route } from "./navigation";

/** Extracts the one-time launch token from a URL hash ("#token=..."), or null. */
export function parseLaunchToken(hash: string) {
  return new URLSearchParams(hash.replace(/^#/, "")).get("token") || null;
}

/** Current route, kept in sync with the browser history. */
export function useRoute() {
  const [current, setCurrent] = useState<Route>(readRoute);
  useEffect(() => {
    const listener = () => setCurrent(readRoute());
    window.addEventListener("popstate", listener);
    return () => window.removeEventListener("popstate", listener);
  }, []);
  return [current, setCurrent] as const;
}

/** Takes the launch token from the URL hash (removing it from the address bar) and
 *  exchanges it for a session. Resolves to an error message, or "" when there was no
 *  token or the exchange worked. */
async function consumeLaunchToken() {
  const launchToken = parseLaunchToken(location.hash);
  if (!launchToken) return "";
  history.replaceState(null, "", location.pathname + location.search);
  try {
    await api("/api/auth", { data: { token: launchToken } });
    return "";
  } catch (e) {
    return (e as Error).message;
  }
}

/** Bootstrap state of the installation: launch-token exchange, a 15 s refresh while the
 *  tab is visible and the palette is closed, and settings writes that are never optimistic. */
export function useBootstrap(paused: { current: boolean }) {
  const [boot, setBoot] = useState<BootstrapDTO | null>(null);
  const [error, setError] = useState("");
  const [auth, setAuth] = useState(false);
  const refreshSequence = useRef(0);

  const refresh = useCallback(async () => {
    const sequence = ++refreshSequence.current;
    try {
      const next = await api<BootstrapDTO>("/api/bootstrap");
      if (sequence !== refreshSequence.current) return;
      setBoot((previous) =>
        JSON.stringify(previous) === JSON.stringify(next) ? previous : next,
      );
      setError("");
      setAuth(false);
    } catch (e) {
      if (sequence !== refreshSequence.current) return;
      if (e instanceof ApiError && e.status === 401) setAuth(true);
      else setError((e as Error).message);
    }
  }, []);

  // Settings writes are never optimistic: the screen keeps showing the state the
  // installation confirmed, and only a receipt with settings replaces it.
  async function saveSettings(patch: SettingsDTO) {
    const receipt = await postSettings(patch);
    if (receipt.settings) {
      setBoot(receipt);
      return;
    }
    const confirmed = await api<BootstrapDTO>("/api/bootstrap");
    if (!confirmed.settings)
      throw new Error("La instalación no confirmó la nueva configuración.");
    setBoot(confirmed);
  }

  useEffect(() => {
    let stopped = false;
    async function start() {
      const failure = await consumeLaunchToken();
      if (failure) {
        setError(failure);
        setAuth(true);
        return;
      }
      if (!stopped) await refresh();
    }
    void start();
    const id = setInterval(() => {
      if (!document.hidden && !paused.current) void refresh();
    }, 15000);
    return () => {
      stopped = true;
      clearInterval(id);
    };
  }, [refresh, paused]);

  return { boot, setBoot, error, setError, auth, refresh, saveSettings };
}
