import { Routes, Route } from 'react-router-dom'
import ProtectedRoute from '@/components/ProtectedRoute'
import Layout from '@/components/Layout'

import Dashboard from '@/routes/Dashboard'
import Resources from '@/routes/Resources'
import ResourcesCreate from '@/routes/Resources/Create'
import ResourcesConfigure from '@/routes/Resources/Configure'
import Connections from '@/routes/Connections'
import ConnectionsSetup from '@/routes/Connections/Setup'
import Agents from '@/routes/Agents'
import AgentsCreate from '@/routes/Agents/Create'
import Guardrails from '@/routes/Guardrails'
import GuardrailsCreate from '@/routes/Guardrails/Create'
import AccessControl from '@/routes/Features/AccessControl'
import AccessControlCreate from '@/routes/Features/AccessControl/Create'
import Runbooks from '@/routes/Features/Runbooks'
import RunbooksSetup from '@/routes/Features/Runbooks/Setup'
import DataMasking from '@/routes/Features/DataMasking'
import DataMaskingCreate from '@/routes/Features/DataMasking/Create'
import IntegrationsAuth from '@/routes/Integrations/Authentication'
import Plugins from '@/routes/Plugins'
import SettingsLicense from '@/routes/Settings/License'
import SettingsInfrastructure from '@/routes/Settings/Infrastructure'
import OrganizationUsers from '@/routes/Organization/Users'
import Sessions from '@/routes/Sessions'
import Reviews from '@/routes/Reviews'
import Login from '@/routes/Auth/Login'
import Signup from '@/routes/Auth/Signup'
import AuthCallback from '@/routes/Auth/Callback'

function Router() {
  return (
    <Routes>
      {/* Public Auth Routes - No Layout */}
      <Route path="/login" element={<Login />} />
      <Route path="/signup" element={<Signup />} />
      <Route path="/auth/callback" element={<AuthCallback />} />

      {/* Protected Routes - With Layout */}
      <Route
        path="/*"
        element={
          <ProtectedRoute>
            <Layout>
              <Routes>
                {/* Dashboard */}
                <Route path="/" element={<Dashboard />} />

                {/* Resources */}
                <Route path="/resources" element={<Resources />} />
                <Route path="/resources/new" element={<ResourcesCreate />} />
                <Route path="/resources/:id/configure" element={<ResourcesConfigure />} />

                {/* Connections */}
                <Route path="/connections" element={<Connections />} />
                <Route path="/connections/setup" element={<ConnectionsSetup />} />

                {/* Agents */}
                <Route path="/agents" element={<Agents />} />
                <Route path="/agents/new" element={<AgentsCreate />} />

                {/* Guardrails */}
                <Route path="/guardrails" element={<Guardrails />} />
                <Route path="/guardrails/new" element={<GuardrailsCreate />} />

                {/* Features */}
                <Route path="/features/access-control" element={<AccessControl />} />
                <Route path="/features/access-control/new" element={<AccessControlCreate />} />
                <Route path="/features/runbooks" element={<Runbooks />} />
                <Route path="/features/runbooks/setup" element={<RunbooksSetup />} />
                <Route path="/features/data-masking" element={<DataMasking />} />
                <Route path="/features/data-masking/new" element={<DataMaskingCreate />} />

                {/* Integrations */}
                <Route path="/integrations/authentication" element={<IntegrationsAuth />} />

                {/* Plugins */}
                <Route path="/plugins" element={<Plugins />} />

                {/* Settings */}
                <Route path="/settings/license" element={<SettingsLicense />} />
                <Route path="/settings/infrastructure" element={<SettingsInfrastructure />} />

                {/* Organization */}
                <Route path="/organization/users" element={<OrganizationUsers />} />

                {/* Sessions & Reviews */}
                <Route path="/sessions" element={<Sessions />} />
                <Route path="/reviews" element={<Reviews />} />
              </Routes>
            </Layout>
          </ProtectedRoute>
        }
      />
    </Routes>
  )
}

export default Router
