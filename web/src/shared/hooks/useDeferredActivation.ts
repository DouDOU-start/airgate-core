import { startTransition, useEffect, useState } from 'react';

function activateDeferred(setActive: (active: boolean) => void) {
  startTransition(() => {
    setActive(true);
  });
}

export function useDeferredActivation(delayMs: number) {
  const [active, setActive] = useState(false);

  useEffect(() => {
    if (typeof window === 'undefined') {
      activateDeferred(setActive);
      return;
    }

    let cancelled = false;
    let timerId: number | null = null;
    const frameId = window.requestAnimationFrame(() => {
      timerId = window.setTimeout(() => {
        if (!cancelled) activateDeferred(setActive);
      }, delayMs);
    });

    return () => {
      cancelled = true;
      window.cancelAnimationFrame(frameId);
      if (timerId != null) window.clearTimeout(timerId);
    };
  }, [delayMs]);

  return active;
}
