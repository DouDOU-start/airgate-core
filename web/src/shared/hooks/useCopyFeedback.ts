import { useCallback, useEffect, useRef, useState } from 'react';

export function useCopyFeedback(durationMs = 2000) {
  const [copied, setCopied] = useState(false);
  const timerRef = useRef<number | null>(null);

  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const resetCopied = useCallback(() => {
    clearTimer();
    setCopied(false);
  }, [clearTimer]);

  const showCopied = useCallback(() => {
    clearTimer();
    setCopied(true);
    timerRef.current = window.setTimeout(() => {
      setCopied(false);
      timerRef.current = null;
    }, durationMs);
  }, [clearTimer, durationMs]);

  useEffect(() => clearTimer, [clearTimer]);

  return { copied, showCopied, resetCopied };
}
