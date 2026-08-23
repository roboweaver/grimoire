import { useEffect, useState } from "react";
import { ForbiddenError } from "./api/client";

export type AsyncState<T> =
  | { status: "loading" }
  | { status: "forbidden" }
  | { status: "error"; message: string }
  | { status: "success"; data: T };

// useAsync runs an aborter-aware loader and maps its outcome to explicit
// loading / forbidden / error / success states (Req 8.3, 8.4). The loader is
// re-run whenever a value in deps changes.
export function useAsync<T>(
  load: (signal: AbortSignal) => Promise<T>,
  deps: unknown[],
): AsyncState<T> {
  const [state, setState] = useState<AsyncState<T>>({ status: "loading" });

  useEffect(() => {
    const ctrl = new AbortController();
    setState({ status: "loading" });
    load(ctrl.signal)
      .then((data) => setState({ status: "success", data }))
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return;
        if (err instanceof ForbiddenError) {
          setState({ status: "forbidden" });
          return;
        }
        const message =
          err instanceof Error ? err.message : "Something went wrong.";
        setState({ status: "error", message });
      });
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return state;
}
