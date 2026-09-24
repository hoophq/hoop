import { FileInput as MantineFileInput } from '@mantine/core'
import { Upload } from 'lucide-react'

/**
 * File picker. Clicking or dropping a file on the field selects it.
 *
 * Usage:
 *   <FileInput label="CSV file" accept=".csv,text/csv" value={file} onChange={setFile} />
 */
export default function FileInput(props) {
  return <MantineFileInput leftSection={<Upload size={16} />} clearable {...props} />
}
