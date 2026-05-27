import { Code as MantineCode } from "@mantine/core"

export default function Code(props) {
  return (
    <MantineCode
      display="inline-block"
      w="fit-content"
      {...props}
    />
  )
}
