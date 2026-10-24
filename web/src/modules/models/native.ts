import { api } from "@/app/api";

/** POST /api/hosts/{id}/magpie/open: this installation answers with the short-lived
 *  address of that host's own Magpie panel and its origin. The address is used at once
 *  and never stored; the only thing that travels in it is the grant the native page
 *  consumes, and no credential goes to the browser storage or to a log. */
export interface NativeOpenDTO {
  url: string;
  origin: string;
}

export function openNative(host: string, view?: string, signal?: AbortSignal) {
  return api<NativeOpenDTO>(`/api/hosts/${encodeURIComponent(host)}/magpie/open`, {
    method: "POST",
    data: view ? { view } : {},
    signal,
  });
}

/** The same panel in a new full tab, in the order the legacy screen used: the blank tab
 *  opens on the click (so the browser lets it through), loses its opener before the real
 *  address arrives, and closes itself if the installation refused the panel. */
export async function openNativeTab(host: string, view?: string) {
  const tab = window.open("about:blank", "_blank");
  if (!tab)
    throw new Error(
      "Permite abrir pestañas para este sitio y vuelve a intentarlo.",
    );
  tab.opener = null;
  try {
    const opened = await openNative(host, view);
    tab.location.replace(opened.url);
  } catch (value) {
    tab.close();
    throw value;
  }
}
