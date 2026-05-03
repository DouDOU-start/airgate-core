import { Calendar, DateField, DatePicker, Description, Label } from '@heroui/react';
import type { DateValue } from '@internationalized/date';
import { parseDate } from '@internationalized/date';
import { useMemo } from 'react';

interface AirGateDatePickerProps {
  className?: string;
  description?: string;
  isRequired?: boolean;
  label: string;
  name?: string;
  onChange: (value: string) => void;
  value?: string;
}

function toDateValue(value?: string): DateValue | null {
  if (!value) return null;
  try {
    return parseDate(value);
  } catch {
    return null;
  }
}

export function AirGateDatePicker({
  className = 'w-full',
  description,
  isRequired,
  label,
  name,
  onChange,
  value,
}: AirGateDatePickerProps) {
  const dateValue = useMemo(() => toDateValue(value), [value]);

  return (
    <DatePicker
      className={className}
      isRequired={isRequired}
      name={name}
      value={dateValue}
      onChange={(nextValue) => onChange(nextValue?.toString() ?? '')}
    >
      <Label>{label}</Label>
      <DateField.Group fullWidth>
        <DateField.Input>
          {(segment) => <DateField.Segment segment={segment} />}
        </DateField.Input>
        <DateField.Suffix>
          <DatePicker.Trigger>
            <DatePicker.TriggerIndicator />
          </DatePicker.Trigger>
        </DateField.Suffix>
      </DateField.Group>
      {description ? <Description>{description}</Description> : null}
      <DatePicker.Popover>
        <Calendar aria-label={label}>
          <Calendar.Header>
            <Calendar.YearPickerTrigger>
              <Calendar.YearPickerTriggerHeading />
              <Calendar.YearPickerTriggerIndicator />
            </Calendar.YearPickerTrigger>
            <Calendar.NavButton slot="previous" />
            <Calendar.NavButton slot="next" />
          </Calendar.Header>
          <Calendar.Grid>
            <Calendar.GridHeader>
              {(day) => <Calendar.HeaderCell>{day}</Calendar.HeaderCell>}
            </Calendar.GridHeader>
            <Calendar.GridBody>
              {(date) => <Calendar.Cell date={date} />}
            </Calendar.GridBody>
          </Calendar.Grid>
          <Calendar.YearPickerGrid>
            <Calendar.YearPickerGridBody>
              {({ year }) => <Calendar.YearPickerCell year={year} />}
            </Calendar.YearPickerGridBody>
          </Calendar.YearPickerGrid>
        </Calendar>
      </DatePicker.Popover>
    </DatePicker>
  );
}
