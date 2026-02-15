import { Title, TextInput, Button, Stack } from '@mantine/core'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAgentStore } from '@/stores/useAgentStore'

function AgentsCreate() {
  const navigate = useNavigate()
  const { createAgent, loading } = useAgentStore()
  const [name, setName] = useState('')

  const handleSubmit = async (e) => {
    e.preventDefault()
    try {
      await createAgent({ name })
      navigate('/agents')
    } catch {
      // error is handled by the store
    }
  }

  return (
    <>
      <Title order={2} mb="lg">New Agent</Title>
      <form onSubmit={handleSubmit}>
        <Stack maw={400}>
          <TextInput
            label="Agent Name"
            placeholder="Enter agent name"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            required
          />
          <Button type="submit" loading={loading}>
            Create Agent
          </Button>
        </Stack>
      </form>
    </>
  )
}

export default AgentsCreate
