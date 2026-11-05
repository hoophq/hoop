import { Braces, Download, FileText, Sheet } from 'lucide-react'
import ActionMenu from '@/components/ActionMenu'
import Button from '@/components/Button'
import { downloadOutput } from './download'

// The section action of the output (Figma "View Options": Download). TXT always;
// CSV and JSON when the output is a table.
export default function DownloadMenu({ tabular, ...props }) {
  return (
    <ActionMenu
      width={200}
      target={
        <Button size="sm" variant="light" color="gray" leftSection={<Download size={16} />}>
          Download
        </Button>
      }
    >
      <ActionMenu.Item leftSection={<FileText size={16} />} onClick={() => downloadOutput(props, 'txt')}>
        Download as TXT
      </ActionMenu.Item>
      {tabular && (
        <>
          <ActionMenu.Item leftSection={<Sheet size={16} />} onClick={() => downloadOutput(props, 'csv')}>
            Download as CSV
          </ActionMenu.Item>
          <ActionMenu.Item leftSection={<Braces size={16} />} onClick={() => downloadOutput(props, 'json')}>
            Download as JSON
          </ActionMenu.Item>
        </>
      )}
    </ActionMenu>
  )
}
