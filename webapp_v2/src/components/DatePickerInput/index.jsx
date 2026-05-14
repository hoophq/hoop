import { DatePickerInput as MantineDatePickerInput } from '@mantine/dates'

/**
 * Date range picker input for filter UIs.
 * Requires @mantine/dates to be installed.
 *
 * Usage — single date:
 *   <DatePickerInput label="Start date" value={date} onChange={setDate} />
 *
 * Usage — date range:
 *   <DatePickerInput type="range" label="Date range" value={range} onChange={setRange} />
 */
export default function DatePickerInput({ type = 'default', ...props }) {
  return <MantineDatePickerInput type={type} radius="sm" clearable {...props} />
}
