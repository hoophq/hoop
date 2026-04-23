import { Box } from '@mantine/core'

function PageLayout({ children }) {
  return (
    <Box p={40} mih="100%">
      {children}
    </Box>
  )
}

export default PageLayout
