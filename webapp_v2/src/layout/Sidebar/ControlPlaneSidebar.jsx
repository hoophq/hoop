import { NAV } from './controlPlaneNav';
import { Sidebar } from './Sidebar';

// No ConfigStatus: that checklist walks the gateway's onboarding.
function ControlPlaneSidebar() {
  return <Sidebar nav={NAV} />;
}

export default ControlPlaneSidebar;
