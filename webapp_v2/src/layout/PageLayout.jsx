import { Box } from '@mantine/core'

function PageLayout({ children }) {
  return (
    <Box p={40} style={{ minHeight: '100%' }}>
      {children}
    </Box>
  )
}

export default PageLayout
