// "Disconnect this browser" in Settings › General and the account menu (#1873, lib/disconnect.ts): it asks first, since
// getting back in takes pairing again (DisconnectConfirm.tsx); the confirm button is busy while the hub revokes the
// credential; a revoke that fails, or a credential the hub cannot revoke from here, asks before signing out instead of
// signing out as if it had worked.
import { Suspense, useState } from "react";
import { handleUnauthorized } from "../lib/api";
import { revokeThisBrowser, signOutHere } from "../lib/disconnect";
import { lazyPage } from "../lib/lazy-page";
import { Button, Dialog, toast } from "../ui/atoms";
import { t } from "../i18n";
import { OverlayBoundary } from "./LazyOverlay";

type Problem = { error: string; id: string; nothing?: never } | { nothing: true; error?: never; id?: never };

// loaded once the browser is idle, so it opens at once
const DisconnectConfirm = lazyPage(() => import("./DisconnectConfirm").then((m) => m.DisconnectConfirm), { idle: true });

export function useDisconnect(): { start: () => void; busy: boolean; dialog: React.ReactNode } {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  // Undefined until first opened; false keeps the question mounted so it closes animated and can retry its chunk.
  const [asking, setAsking] = useState<boolean>();
  const ask = () => !busy && setAsking(true);
  const revoke = async () => {
    if (busy) return;
    setBusy(true);
    // signing out on purpose: a request that gets a 401 meanwhile must not show "this browser was signed out"
    const before = handleUnauthorized(() => {});
    const r = await revokeThisBrowser();
    if (r.ok) return signOutHere();
    handleUnauthorized(before); // still signed in here until the user says otherwise
    setBusy(false);
    setAsking(false);
    setProblem(r);
  };
  const close = () => !busy && setProblem(null);
  const signOut = () => {
    handleUnauthorized(() => {});
    signOutHere(problem?.id ?? "");
  };
  const nothing = problem?.nothing;
  const dialog = (
    <>
      {/* its chunk may not load (the hub was redeployed, or it is offline): the host stays, nothing is revoked, the toast
          says why nothing opened, and the next Disconnect tries the chunk again */}
      {asking !== undefined && (
        <OverlayBoundary open={!!asking} onClose={() => { setAsking(false); toast(t("pageboundary.chunk_body"), "error"); }}>
          <Suspense fallback={null}>
            <DisconnectConfirm open={asking} busy={busy} onCancel={() => setAsking(false)} onConfirm={() => void revoke()} />
          </Suspense>
        </OverlayBoundary>
      )}
      <Dialog
        open={!!problem}
        onClose={close}
        title={nothing ? t("disconnect.nothing_title") : t("disconnect.failed_title")}
        footer={
          <>
            <Button tone="quiet" onClick={close} disabled={busy}>
              {t("common.cancel")}
            </Button>
            <Button tone="danger" onClick={signOut} disabled={!nothing && busy}>
              {t(nothing ? "disconnect.sign_out" : "disconnect.sign_out_anyway")}
            </Button>
            {!nothing && <Button tone="primary" onClick={() => void revoke()} busy={busy}>
              {t("disconnect.retry")}
            </Button>}
          </>
        }
      >
        <p className="text-[13px] leading-relaxed text-ink-2">{nothing ? t("disconnect.nothing_body") : t("disconnect.failed_body")}</p>
        {/* why, in the hub's (or the page's) own words */}
        {problem?.error !== undefined && <p className="mt-2 text-[12px] leading-relaxed text-ink-3">{problem.error}</p>}
      </Dialog>
    </>
  );
  return { start: ask, busy, dialog };
}
