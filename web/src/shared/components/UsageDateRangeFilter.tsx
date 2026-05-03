import { Button, DateField, DateRangePicker, Label, RangeCalendar } from '@heroui/react';
import type { DateValue } from '@internationalized/date';
import { parseDate } from '@internationalized/date';
import { useMemo } from 'react';
import { X } from 'lucide-react';

interface UsageDateRangeFilterProps {
  clearLabel?: string;
  endDate?: string;
  label: string;
  onChange: (startDate: string, endDate: string) => void;
  startDate?: string;
}

type DateRangeValue = {
  end: DateValue;
  start: DateValue;
} | null;

function toDateValue(value?: string): DateValue | null {
  if (!value) return null;
  try {
    return parseDate(value);
  } catch {
    return null;
  }
}

export function UsageDateRangeFilter({
  clearLabel = 'Clear',
  endDate,
  label,
  onChange,
  startDate,
}: UsageDateRangeFilterProps) {
  const value = useMemo<DateRangeValue>(() => {
    const start = toDateValue(startDate);
    const end = toDateValue(endDate);
    return start && end ? { start, end } : null;
  }, [endDate, startDate]);

  return (
    <DateRangePicker
      aria-label={label}
      className="ag-usage-date-range w-full sm:w-72"
      endName="endDate"
      startName="startDate"
      value={value}
      onChange={(nextValue) => {
        onChange(nextValue?.start?.toString() ?? '', nextValue?.end?.toString() ?? '');
      }}
    >
      <Label className="sr-only">{label}</Label>
      <DateField.Group fullWidth>
        <DateField.Input slot="start">
          {(segment) => <DateField.Segment segment={segment} />}
        </DateField.Input>
        <DateRangePicker.RangeSeparator />
        <DateField.Input slot="end">
          {(segment) => <DateField.Segment segment={segment} />}
        </DateField.Input>
        <DateField.Suffix>
          {value ? (
            <Button
              aria-label={clearLabel}
              className="ag-date-range-clear"
              isIconOnly
              size="sm"
              type="button"
              variant="ghost"
              onPress={() => onChange('', '')}
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          ) : null}
          <DateRangePicker.Trigger>
            <DateRangePicker.TriggerIndicator />
          </DateRangePicker.Trigger>
        </DateField.Suffix>
      </DateField.Group>
      <DateRangePicker.Popover>
        <RangeCalendar aria-label={label}>
          <RangeCalendar.Header>
            <RangeCalendar.YearPickerTrigger>
              <RangeCalendar.YearPickerTriggerHeading />
              <RangeCalendar.YearPickerTriggerIndicator />
            </RangeCalendar.YearPickerTrigger>
            <RangeCalendar.NavButton slot="previous" />
            <RangeCalendar.NavButton slot="next" />
          </RangeCalendar.Header>
          <RangeCalendar.Grid>
            <RangeCalendar.GridHeader>
              {(day) => <RangeCalendar.HeaderCell>{day}</RangeCalendar.HeaderCell>}
            </RangeCalendar.GridHeader>
            <RangeCalendar.GridBody>
              {(date) => <RangeCalendar.Cell date={date} />}
            </RangeCalendar.GridBody>
          </RangeCalendar.Grid>
          <RangeCalendar.YearPickerGrid>
            <RangeCalendar.YearPickerGridBody>
              {({ year }) => <RangeCalendar.YearPickerCell year={year} />}
            </RangeCalendar.YearPickerGridBody>
          </RangeCalendar.YearPickerGrid>
        </RangeCalendar>
      </DateRangePicker.Popover>
    </DateRangePicker>
  );
}
