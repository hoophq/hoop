import api from './api'
import { authService } from './auth'

const license = {
  getInfo: () => authService.getServerInfo().then((data) => data?.license_info ?? null),
  update: (payload) => api.put('/orgs/license', payload).then((r) => r.data),
}

export default license
