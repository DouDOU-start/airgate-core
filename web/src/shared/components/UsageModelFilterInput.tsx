import { memo, useEffect, useRef, useState } from 'react';
import { Input, TextField as HeroTextField } from '@heroui/react';
import { Search } from 'lucide-react';
import { useDebouncedValue } from '../hooks/useDebouncedValue';

interface UsageModelFilterInputProps {
  ariaLabel: string;
  debounceMs?: number;
  onModelChange: (model: string) => void;
  placeholder: string;
  value?: string;
}

export const UsageModelFilterInput = memo(function UsageModelFilterInput({
  ariaLabel,
  debounceMs = 250,
  onModelChange,
  placeholder,
  value = '',
}: UsageModelFilterInputProps) {
  const [inputValue, setInputValue] = useState(value);
  const debouncedValue = useDebouncedValue(inputValue.trim(), debounceMs);
  const lastEmittedValueRef = useRef(value.trim());

  useEffect(() => {
    setInputValue((current) => (current === value ? current : value));
    lastEmittedValueRef.current = value.trim();
  }, [value]);

  useEffect(() => {
    if (debouncedValue === lastEmittedValueRef.current) return;
    lastEmittedValueRef.current = debouncedValue;
    onModelChange(debouncedValue);
  }, [debouncedValue, onModelChange]);

  return (
    <HeroTextField fullWidth>
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
        <Input
          aria-label={ariaLabel}
          className="pl-9"
          placeholder={placeholder}
          value={inputValue}
          onChange={(event) => setInputValue(event.target.value)}
        />
      </div>
    </HeroTextField>
  );
});
